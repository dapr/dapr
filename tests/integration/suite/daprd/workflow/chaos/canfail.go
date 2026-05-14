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
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(canfail))
}

// canfail verifies that the orchestrator recovers when ScheduleJob (the gRPC
// the scheduler uses to register a reminder) fails for the new-event wake-up
// reminder that addWorkflowEvent issues for an incoming activity result.
// The failure is injected via a proxy that fronts the real scheduler so
// the test can arm specific gRPC failures on specific methods, much like
// the pluggable state-store fault wrapper does for state.Multi.
type canfail struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (c *canfail) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)
	c.proxy = proxy.New(t, c.scheduler)

	c.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(c.scheduler),
		workflow.WithSchedulerAddress(c.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(c.scheduler, c.proxy, c.workflow),
	}
}

func (c *canfail) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const wfID = "canfail-wf"

	activityStarted := make(chan struct{})
	releaseActivity := make(chan struct{})

	r := c.workflow.Registry()

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

	c.workflow.BackendClient(t, ctx)
	gclient := c.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// Wait until the activity is in flight before arming the proxy, so the
	// failure lands on the ScheduleJob that addWorkflowEvent makes for the
	// activity-result wake-up reminder rather than on the workflow-start
	// or activity-schedule reminders.
	select {
	case <-activityStarted:
	case <-time.After(15 * time.Second):
		require.Fail(t, "activity did not start")
	}

	failedCh := make(chan struct{})
	// codes.Internal is non-transient so daprd's CreateReminderWithRetry
	// does not mask it; the orchestrator's error path runs.
	c.proxy.ArmFailures(proxy.MethodScheduleJob, 1, codes.Internal, failedCh)

	close(releaseActivity)

	select {
	case <-failedCh:
	case <-time.After(15 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired")
	}

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        wfID,
			WorkflowComponent: "dapr",
		})
		if assert.NoError(co, gerr) {
			assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 100*time.Millisecond)

	assert.GreaterOrEqual(t, c.proxy.FailedCount(), 1,
		"expected the injected ScheduleJob failure to have fired at least once")
}
