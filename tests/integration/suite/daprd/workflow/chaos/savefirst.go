/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chaos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(savefirst))
}

// savefirst verifies that addWorkflowEvent saves the inbox event to the
// state store BEFORE creating the wake-up reminder. The reminder's dueTime
// is anchored at the workflow's start (in the past), so the scheduler fires
// it immediately on Create. Under placement rebalance the firing daprd may
// not be the host that ran AddWorkflowEvent, and an empty-inbox SUCCESS ack
// would delete the reminder before the save commits, stranding the inbox row.
//
// With save-first, an inbox-save failure must NOT leave an orphan reminder
// in the scheduler. The recovery path is the activity actor's retry-forever
// run-activity reminder, which re-fires AddWorkflowEvent until the save lands.
type savefirst struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
	sched    *scheduler.Scheduler
}

func (s *savefirst) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	s.store = fault.New(t)

	sock := socket.New(t)
	s.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(s.store),
	)

	s.sched = scheduler.New(t)

	s.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithSchedulerInstance(s.sched),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, s.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.ss, s.workflow),
	}
}

func (s *savefirst) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "savefirst-wf"

	activityStarted := make(chan struct{}, 1)
	releaseActivity := make(chan struct{})

	r := s.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		activityStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseActivity:
			return nil, nil
		}
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	select {
	case <-activityStarted:
	case <-time.After(15 * time.Second):
		require.Fail(t, "activity did not start")
	}

	failedCh := make(chan struct{})
	s.store.ArmFailures(wfID+"||inbox-", 1, failedCh)

	close(releaseActivity)

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected inbox-save failure never fired")
	}

	// At the moment the inbox save just failed, no wake-up reminder for the
	// activity-result event may exist in the scheduler. Reminder-first
	// ordering would have already registered new-event-tc-0 by this point.
	ns := s.workflow.Dapr().Namespace()
	appID := s.workflow.Dapr().AppID()
	prefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||%s||dapr.internal.%s.%s.workflow||%s||new-event-tc-",
		ns, ns, appID, wfID,
	)
	require.Empty(t, s.sched.ListAllKeys(t, ctx, prefix),
		"wake-up reminder must not exist while inbox save is unwritten")

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, s.store.FailedCount(), 1,
		"expected the injected save failure to have fired")
}
