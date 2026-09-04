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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(eventclaimcancel))
}

// eventclaimcancel is the external-event counterpart of startclaimcancel: a
// RaiseEvent commits its inbox row while the Scheduler is unreachable and a
// dissemination cancels the claim context before the new-event reminder is
// registered. The client sees an error and never re-raises. The event must
// still be delivered once the Scheduler is reachable again.
type eventclaimcancel struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
	appID     string
}

func (e *eventclaimcancel) Setup(t *testing.T) []framework.Option {
	if workflow.FastPathFromEnv() {
		t.Skip("WorkflowsFastPath drives the external-event wake-up locally and never issues the per-event ScheduleJob this test arms a failure on")
	}

	e.appID = uuid.New().String()
	e.scheduler = scheduler.New(t)
	e.proxy = proxy.New(t, e.scheduler)
	e.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(e.scheduler),
		workflow.WithSchedulerAddress(e.proxy.Address()),
		workflow.WithDaprdOptions(0, daprd.WithAppID(e.appID)),
	)
	return []framework.Option{
		framework.WithProcesses(e.scheduler, e.proxy, e.workflow),
	}
}

func (e *eventclaimcancel) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const wfID = "eventclaimcancel-wf"
	wfFn := func(octx *task.WorkflowContext) (any, error) {
		if err := octx.WaitForSingleEvent("go", -1).Await(nil); err != nil {
			return nil, err
		}
		return "delivered", nil
	}
	require.NoError(t, e.workflow.Registry().AddWorkflowN("wf", wfFn))
	cl := e.workflow.BackendClient(t, ctx)
	gclient := e.workflow.GRPCClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(wfID))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	startVersion := e.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	failedCh := make(chan struct{})
	e.proxy.ArmFailures(proxy.MethodScheduleJob, 1_000_000, codes.Unavailable, failedCh)

	raiseErr := make(chan error, 1)
	go func() {
		rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		raiseErr <- cl.RaiseEvent(rctx, id, "go")
	}()

	select {
	case <-failedCh:
	case <-time.After(30 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired")
	}

	extra := daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(e.appID),
		daprd.WithPlacementAddresses(e.workflow.Placement().Address()),
		daprd.WithSchedulerAddressesReset(e.proxy.Address()),
		daprd.WithResourceFiles(e.workflow.DB().GetComponent(t)),
	}, e.workflow.FeatureOptions(t)...)...)
	extra.Run(t, ctx)
	t.Cleanup(func() { extra.Cleanup(t) })
	extra.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("wf", wfFn))
	extraClient := client.NewTaskHubGrpcClient(extra.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, extraClient.StartWorkItemListener(ctx, registry))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := e.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		assert.Greater(c, table.Version, startVersion, "placement table version must advance for the new daprd")
	}, 15*time.Second, 10*time.Millisecond)

	select {
	case rerr := <-raiseErr:
		require.Error(t, rerr, "the raise must fail while the Scheduler is unreachable")
	case <-time.After(60 * time.Second):
		require.Fail(t, "raise did not return after the client deadline")
	}

	sctx, scancel := context.WithTimeout(ctx, 5*time.Second)
	resp, err := gclient.GetWorkflowBeta1(sctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
	scancel()
	require.NoError(t, err)
	require.Equal(t, "RUNNING", resp.GetRuntimeStatus())
	require.Zero(t, e.scheduler.JobKeyCount(t, ctx, "||new-event-"), "no wake-up reminder can exist during the outage")
	assert.Positive(t, e.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:reminder_arm_detached"),
		"the cancelled claim must have handed the wake-up reminder create to the detached retry")

	e.proxy.ArmFailures(proxy.MethodScheduleJob, 0, codes.Unavailable, nil)

	for _, c := range []rtv1.DaprClient{gclient, extra.GRPCClient(t, ctx)} {
		assert.EventuallyWithT(t, func(co *assert.CollectT) {
			resp, gerr := c.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
			if assert.NoError(co, gerr) {
				assert.Equal(co, "COMPLETED", resp.GetRuntimeStatus())
			}
		}, 60*time.Second, 50*time.Millisecond, "the committed event must be delivered once the Scheduler is reachable, without a client re-raise")
	}

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, `"delivered"`, meta.GetOutput().GetValue())
}
