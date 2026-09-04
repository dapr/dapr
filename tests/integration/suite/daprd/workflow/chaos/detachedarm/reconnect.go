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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reconnect))
}

// reconnect: the detached retry that arms a committed start's reminder
// must outlive the app's work item stream. The workflow actor types are
// registered per stream connection, so a reconnect tears the actor factories
// down and rebuilds them while the sidecar keeps running; the retry runs on
// the runtime lifetime and completes once the Scheduler is back, without a
// client create and without a status read.
type reconnect struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (s *reconnect) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	s.proxy = proxy.New(t, s.scheduler)
	s.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(s.scheduler),
		workflow.WithSchedulerAddress(s.proxy.Address()),
		// Keep the status-read re-drive out of this test.
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "5m",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.proxy, s.workflow),
	}
}

func (s *reconnect) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "reconnect-wf"
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("wf", func(*task.WorkflowContext) (any, error) {
		return "reconnected", nil
	}))
	d := s.workflow.Dapr()
	gclient := d.GRPCClient(t, ctx)

	// A worker connection the test controls: its stream context registers
	// the workflow actor types, and cancelling it unregisters them.
	connect := func() context.CancelFunc {
		wctx, cancel := context.WithCancel(ctx)
		cl := client.NewTaskHubGrpcClient(d.GRPCConn(t, ctx), logger.New(t))
		require.NoError(t, cl.StartWorkItemListener(wctx, registry))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.GreaterOrEqual(c, len(d.GetMetaActorRuntime(c, ctx).ActiveActors), 3, "workflow actor types must be registered")
		}, 20*time.Second, 10*time.Millisecond)
		return cancel
	}
	disconnect := connect()

	failedCh := make(chan struct{})
	s.proxy.ArmFailures(proxy.MethodScheduleJob, 1_000_000, codes.Unavailable, failedCh)

	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err := gclient.StartWorkflowBeta1(cctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	cancel()
	require.Error(t, err, "the create must fail while the Scheduler is unreachable")
	select {
	case <-failedCh:
	case <-time.After(10 * time.Second):
		require.Fail(t, "injected ScheduleJob failure never fired")
	}
	require.Positive(t, d.Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:reminder_arm_detached"),
		"the abandoned create must have been handed to the detached retry")

	// The app reconnects: the actor types are unregistered and registered
	// again, rebuilding the factories under the retry.
	disconnect()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, d.GetMetaActorRuntime(c, ctx).ActiveActors, "workflow actor types must be unregistered with no worker connected")
	}, 20*time.Second, 10*time.Millisecond)
	disconnect = connect()
	t.Cleanup(disconnect)

	s.proxy.ArmFailures(proxy.MethodScheduleJob, 0, codes.Unavailable, nil)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: wfID, WorkflowComponent: "dapr"})
		if assert.NoError(c, gerr) {
			assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 60*time.Second, 50*time.Millisecond, "the detached retry must survive the worker reconnect and drive the committed start")
}
