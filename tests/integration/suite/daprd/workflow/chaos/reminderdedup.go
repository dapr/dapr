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
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminderdedup))
}

// reminderdedup verifies that repeated addWorkflowEvent retries for the same
// activity result collapse onto a single scheduler entry (the wake-up reminder
// is named deterministically per event so the scheduler overwrites by name)
// instead of accumulating under random suffixes.
type reminderdedup struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (s *reminderdedup) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	s.store = fault.New(t)

	sock := socket.New(t)
	s.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(s.store),
	)

	s.workflow = workflow.New(t,
		workflow.WithNoDB(),
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
		framework.WithProcesses(s.ss, s.workflow),
	}
}

func (s *reminderdedup) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "reminderdedup-wf"

	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})

	r := s.workflow.Registry()

	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		activityStarted <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseActivity:
			return "done", nil
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

	s.store.ArmFailures(wfID+"||inbox-", 3, nil)

	close(releaseActivity)

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, s.store.FailedCount(), 3,
		"expected three injected save failures to have fired")

	// The deterministic reminder name means even though we retried at least
	// three times, no leftover new-event reminders remain in the scheduler after
	// the workflow completes (the runWorkflow path deletes its own reminder
	// after it drains the inbox).
	newEventPrefix := fmt.Sprintf(
		"dapr/jobs/actorreminder||default||dapr.internal.default.%s.workflow||%s||new-event-",
		s.workflow.Dapr().AppID(), wfID,
	)
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		keys := s.workflow.Scheduler().ListAllKeys(t, ctx, newEventPrefix)
		assert.Empty(co, keys, "expected no leftover new-event reminders; got %v", keys)
	}, 15*time.Second, 10*time.Millisecond)
}
