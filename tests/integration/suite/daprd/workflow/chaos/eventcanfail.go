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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(eventcanfail))
}

// eventcanfail is the external-event analogue of canfail. It verifies that the
// orchestrator recovers when ScheduleJob fails for the new-event wake-up
// reminder that addWorkflowEvent issues for an incoming *external* event
// (RaiseEvent), not an activity result.
type eventcanfail struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (e *eventcanfail) Setup(t *testing.T) []framework.Option {
	e.scheduler = scheduler.New(t)
	e.proxy = proxy.New(t, e.scheduler)

	e.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(e.scheduler),
		workflow.WithSchedulerAddress(e.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(e.scheduler, e.proxy, e.workflow),
	}
}

func (e *eventcanfail) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "eventcanfail-wf"

	r := e.workflow.Registry()

	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		if err := octx.WaitForSingleEvent("proceed", -1).Await(nil); err != nil {
			return nil, err
		}
		return "done", nil
	}))

	cl := e.workflow.BackendClient(t, ctx)
	gclient := e.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// Wait until the workflow has started (and thus its start reminder's
	// ScheduleJob has already completed) before arming the proxy, so the
	// injected failure lands on the new-event reminder for the external event
	// rather than on the workflow-start reminder.
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "RUNNING", resp.GetRuntimeStatus())
		}
	}, 15*time.Second, 10*time.Millisecond)

	failedCh := make(chan struct{})
	// codes.Internal is non-transient so daprd's CreateReminderWithRetry does
	// not mask it; the orchestrator's error path runs and the new-event reminder
	// is genuinely not created on this attempt.
	e.proxy.ArmFailures(proxy.MethodScheduleJob, 1, codes.Internal, failedCh)

	// RaiseEvent saves the event to the inbox, then tries to create the
	// new-event reminder, whose ScheduleJob the proxy fails. The error
	// propagates back here; the inbox row is durable regardless.
	rerr := cl.RaiseEvent(ctx, api.InstanceID(wfID), "proceed")
	require.Error(t, rerr)

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired on the new-event reminder")
	}

	// The workflow must still complete: a saved external-event inbox row must
	// not be stranded by a transient scheduler failure.
	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 10*time.Millisecond)

	assert.GreaterOrEqual(t, e.proxy.FailedCount(), 1,
		"expected the injected ScheduleJob failure to have fired at least once")
}
