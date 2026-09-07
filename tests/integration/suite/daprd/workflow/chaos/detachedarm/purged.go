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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(purged))
}

// purged: a detached retry arming a committed start's reminder must
// stop once the instance is purged. It re-reads the durable state before each
// attempt, so the Scheduler coming back after the purge must not produce a
// start reminder, a fire, or an execution for the purged instance.
type purged struct {
	workflow  *workflow.Workflow
	scheduler *scheduler.Scheduler
	proxy     *proxy.Proxy
}

func (s *purged) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	s.proxy = proxy.New(t, s.scheduler)
	s.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(s.scheduler),
		workflow.WithSchedulerAddress(s.proxy.Address()),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "5m",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(s.scheduler, s.proxy, s.workflow),
	}
}

func (s *purged) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const wfID = "purged-wf"
	var executions atomic.Int64
	require.NoError(t, s.workflow.Registry().AddWorkflowN("wf", func(*task.WorkflowContext) (any, error) {
		executions.Add(1)
		return "purged-never", nil
	}))
	cl := s.workflow.BackendClient(t, ctx)
	gclient := s.workflow.GRPCClient(t, ctx)
	d := s.workflow.Dapr()

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

	// The stranded instance is force purged while the retry is in flight.
	require.NoError(t, cl.PurgeWorkflowState(ctx, api.InstanceID(wfID), api.WithForcePurge(true)))
	require.Zero(t, s.workflow.DB().CountStateKeys(t, ctx, wfID), "the purge must remove every row")

	triggered := func() int {
		return int(s.scheduler.Metrics(t, ctx).All()["dapr_scheduler_jobs_triggered_total"])
	}
	baseline := triggered()

	s.proxy.ArmFailures(proxy.MethodScheduleJob, 0, codes.Unavailable, nil)

	// Long enough for several retry attempts against the healthy Scheduler.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		require.Zero(t, s.scheduler.JobKeyCount(t, ctx, "||"+wfID+"||start-es-"), "no start reminder may be registered for the purged instance")
		require.Equal(t, baseline, triggered(), "no reminder may fire for the purged instance")
		require.Zero(t, executions.Load(), "the purged instance must never run")
		time.Sleep(100 * time.Millisecond)
	}
	require.Zero(t, s.workflow.DB().CountStateKeys(t, ctx, wfID), "the purged instance must stay purged")
}
