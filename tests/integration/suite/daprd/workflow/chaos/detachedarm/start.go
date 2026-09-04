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

package detachedarm

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
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(start))
}

// start reproduces a field-reported stranding: a create commits
// its ExecutionStarted inbox row while the Scheduler is unreachable, and a
// placement dissemination for the workflow actor type (a second daprd joining
// the same app) cancels the in-flight claim context before the start reminder
// is registered. The client sees an error and never re-issues the create. The
// instance must still run once the Scheduler is reachable again: the committed
// row needs a driver that is as durable as the row itself, not one bounded by
// the claim context of the invocation that created it.
type start struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
	appID     string
}

func (s *start) Setup(t *testing.T) []framework.Option {
	s.appID = uuid.New().String()
	s.scheduler = scheduler.New(t)
	s.proxy = proxy.New(t, s.scheduler)
	s.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(s.scheduler),
		workflow.WithSchedulerAddress(s.proxy.Address()),
		workflow.WithDaprdOptions(0, daprd.WithAppID(s.appID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.proxy, s.workflow),
	}
}

func (s *start) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "start-wf"
	wfFn := func(*task.WorkflowContext) (any, error) {
		return "started", nil
	}
	require.NoError(t, s.workflow.Registry().AddWorkflowN("wf", wfFn))
	s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)

	startVersion := s.workflow.Placement().PlacementTables(t, ctx).Tables["default"].Version

	// Scheduler outage: every ScheduleJob fails with a transient code, which
	// the create retries with backoff, and every GetJob fails too, so the
	// retried create's reminder-missing check cannot reach the Scheduler
	// either (the report observed exactly this pairing).
	failedCh := make(chan struct{})
	s.proxy.ArmFailures(proxy.MethodScheduleJob, 1_000_000, codes.Unavailable, failedCh)
	s.proxy.ArmFailures(proxy.MethodGetJob, 1_000_000, codes.Unavailable, nil)

	// The client gives up on the create well before the Scheduler is back,
	// as an SDK call with a request timeout would.
	createErr := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_, err := gclient.StartWorkflowBeta1(cctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "wf",
			InstanceId:        wfID,
		})
		createErr <- err
	}()

	// The first failed ScheduleJob is the start reminder: the inbox row is
	// already committed (save-first), the create is now in its retry loop.
	select {
	case <-failedCh:
	case <-time.After(30 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired")
	}

	// A second daprd for the same app changes the workflow actor type's hash
	// ring: dissemination cancels the in-flight claim context the create is
	// running under.
	extra := daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(s.appID),
		daprd.WithPlacementAddresses(s.workflow.Placement().Address()),
		daprd.WithSchedulerAddressesReset(s.proxy.Address()),
		daprd.WithResourceFiles(s.workflow.DB().GetComponent(t)),
	}, s.workflow.FeatureOptions(t)...)...)
	extra.Run(t, ctx)
	t.Cleanup(func() { extra.Cleanup(t) })
	extra.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("wf", wfFn))
	extraClient := client.NewTaskHubGrpcClient(extra.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, extraClient.StartWorkItemListener(ctx, registry))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.workflow.Placement().PlacementTables(t, ctx).Tables["default"]
		if !assert.NotNil(c, table) {
			return
		}
		assert.Greater(c, table.Version, startVersion, "placement table version must advance for the new daprd")
	}, 15*time.Second, 10*time.Millisecond)

	// The claim cancel abandons the create and its retries cannot register
	// the reminder before the client gives up: the client gets an error and,
	// like a status-guarding client, never re-issues the create.
	select {
	case err := <-createErr:
		require.Error(t, err, "the create must fail while the Scheduler is unreachable")
	case <-time.After(60 * time.Second):
		require.Fail(t, "create did not return after the client deadline")
	}

	// The status read must not wait on the actor.
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	resp, err := gclient.GetWorkflowBeta1(rctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
	rcancel()
	require.NoError(t, err)
	require.Equal(t, "PENDING", resp.GetRuntimeStatus(), "the committed start must be visible as PENDING")
	require.Zero(t, s.scheduler.JobKeyCount(t, ctx, "||start-es-"), "no start reminder can exist during the outage")
	assert.Positive(t, s.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:reminder_arm_detached"),
		"the cancelled claim must have handed the start reminder create to the detached retry")

	// Scheduler back.
	s.proxy.ArmFailures(proxy.MethodScheduleJob, 0, codes.Unavailable, nil)
	s.proxy.ArmFailures(proxy.MethodGetJob, 0, codes.Unavailable, nil)

	for _, cl := range []rtv1.DaprClient{gclient, extra.GRPCClient(t, ctx)} {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, gerr := cl.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
			if assert.NoError(c, gerr) {
				assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
			}
		}, 60*time.Second, 50*time.Millisecond, "the committed start must be driven once the Scheduler is reachable, without a client create")
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, s.scheduler.JobKeyCount(t, ctx, "||start-es-"))
	}, 30*time.Second, 50*time.Millisecond, "the one-shot start reminder must be consumed")
}
