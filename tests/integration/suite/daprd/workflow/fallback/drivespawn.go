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

package fallback

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(drivespawn))
}

// drivespawn raises an event under WorkflowsFastPath and holds the local
// drive the raise arms between its fast path check and its spawn, while the
// last worker disconnects and the daprd turns into a fallback host in place.
// The drive must not start on the fallback host, where its turns would fail
// for want of an executor: the janitor delivers the event once a worker
// connects again.
type drivespawn struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

const (
	drivespawnHold     = "TEST INJECTION: holding the local drive spawn"
	drivespawnFallback = "The last workflow executor of this daprd left"
)

func (d *drivespawn) Setup(t *testing.T) []framework.Option {
	d.logline = logline.New(t, logline.WithCaptureAll())
	d.workflow = workflow.New(t,
		workflow.WithFastPath(true),
		workflow.WithStagedOperations(true),
		workflow.WithFallbackSettleN(t, 0),
		workflow.WithDaprdOptions(0,
			daprd.WithWorkflowJanitorPeriod(t, time.Second*2),
			daprd.WithExecOptions(
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_DRIVE_SPAWN_HOLD", "5s"),
				exec.WithStdout(d.logline.Stdout()),
				exec.WithStderr(d.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, d.workflow),
	}
}

func (d *drivespawn) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, d.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	worker := d.workflow.ConnectWorker(t, ctx, d.workflow.Registry())
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)

	mgmt := d.workflow.ManagementClient(t, ctx)
	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)
	fworkflow.WaitForRuntimeStatus(t, ctx, mgmt, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING)

	failedWakes := func() float64 {
		return d.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:failed")
	}
	before := failedWakes()

	// The raise passes the fast path check with the worker connected, and
	// its drive waits out the hold.
	holds := d.logline.Count(drivespawnHold)
	raised := make(chan error, 1)
	go func() { raised <- mgmt.RaiseEvent(ctx, id, "go") }()
	require.Eventually(t, func() bool {
		return d.logline.Count(drivespawnHold) > holds
	}, time.Second*10, time.Millisecond*10, "the raise did not arm a local drive")

	worker.Disconnect(t)
	d.workflow.WaitForNoConnectedWorkers(t, ctx)
	require.Eventually(t, func() bool {
		return d.logline.Contains(drivespawnFallback)
	}, time.Second*10, time.Millisecond*10, "the daprd did not turn into a fallback host")

	select {
	case err := <-raised:
		require.FailNow(t, "the drive left its hold before the daprd turned into a fallback host", "raise: %v", err)
	default:
	}

	select {
	case err := <-raised:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		require.FailNow(t, "the raise did not return")
	}

	// A drive that started here would have failed its first turn at once.
	time.Sleep(time.Second * 2)
	require.InDelta(t, before, failedWakes(), 0, "a local drive ran on the fallback host")

	d.workflow.ConnectWorker(t, ctx, d.workflow.Registry())
	fworkflow.WaitForRuntimeStatus(t, ctx, mgmt, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
}
