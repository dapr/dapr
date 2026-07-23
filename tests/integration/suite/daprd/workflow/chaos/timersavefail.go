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
	suite.Register(new(timersavefail))
}

// timersavefail verifies that when the orchestrator's signAndSaveState
// transiently fails on the run that scheduled a TimerCreated event, the
// workflow still completes via the reminder-retry path.
//
// Sequence at risk:
//  1. Workflow code yields CreateTimer.
//  2. createTimers registers the timer reminder with the scheduler.
//  3. signAndSaveState fails (state store transient error).
//
// At step 3 the timer reminder is already on the scheduler but the on-disk
// history does NOT yet record TimerCreated. The reminder must fire and
// runWorkflow must retry until the save succeeds; if the error returned
// from runWorkflow isn't wrapped as Recoverable, runWorkflowFromReminder's
// default branch leaves the dispatcher without the same retry semantics
// the other error paths in the same function rely on, and the workflow
// can remain parked in RUNNING indefinitely.
type timersavefail struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (s *timersavefail) Setup(t *testing.T) []framework.Option {
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

func (s *timersavefail) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "timersavefail-wf"

	r := s.workflow.Registry()
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		if err := octx.CreateTimer(500 * time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	}))

	s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	// Arm a single failure on the first Multi that touches a "history-" key
	// for this workflow. The save that records the TimerCreated event in
	// history is the first one to write a history- key, and it runs AFTER
	// createTimers has already registered the reminder with the scheduler.
	// That's exactly the gap the fix protects against.
	failedCh := make(chan struct{})
	s.store.ArmFailures(wfID+"||history-", 1, failedCh)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected save failure never fired")
	}

	// The workflow's only durable wait is the 500ms timer, so on the retry
	// path it should complete in well under 30s. If the save-error retry
	// path is broken, the workflow stays in RUNNING forever and this
	// assertion times out.
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 100*time.Millisecond)

	assert.GreaterOrEqual(t, s.store.FailedCount(), 1,
		"expected the injected save failure to have fired")
}
