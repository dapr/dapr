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

package fold

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(payloadfanin))
}

// payloadfanin pins the payload stall guard against fold-held completions:
// a fan-in of large results taken by one folding turn must stall gracefully,
// not blow the hard gRPC limit. Fold drives are dropped via the test
// injection so the results accumulate into a single recovery turn.
type payloadfanin struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (p *payloadfanin) Setup(t *testing.T) []framework.Option {
	p.place = placement.New(t)
	p.scheduler = procscheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	p.daprd = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(p.place.Address()),
		daprd.WithSchedulerAddresses(p.scheduler.Address()),
		daprd.WithMaxBodySize("1Mi"),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_JANITOR_PERIOD", "2s",
			"DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES", "100",
		)),
	)

	return []framework.Option{
		framework.WithProcesses(p.place, p.scheduler, db, p.daprd),
	}
}

func (p *payloadfanin) Run(t *testing.T, ctx context.Context) {
	p.scheduler.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)
	p.daprd.WaitUntilRunning(t, ctx)

	// 4 x 300 KiB: the total exceeds the 1 MiB hard limit, but any strict
	// subset fits under the 95% stall threshold, so no split of the fan-in
	// can stall via durable history alone. Only a guard that counts the
	// fold-held results can stall this workflow.
	const chunkSize = 300 * 1024
	const numActivities = 4
	chunk := strings.Repeat("x", chunkSize)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("fanin", func(wctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, numActivities)
		for i := range tasks {
			tasks[i] = wctx.CallActivity("emit-chunk")
		}
		for _, tk := range tasks {
			var out string
			if err := tk.Await(&out); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
		return chunk, nil
	}))

	cl := client.NewTaskHubGrpcClient(p.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	id, err := cl.ScheduleNewWorkflow(ctx, "fanin")
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, cl, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, cl, id)
	require.NotNil(t, lastEvent)
	require.Equal(t, protos.StalledReason_PAYLOAD_SIZE_EXCEEDED, lastEvent.GetExecutionStalled().GetReason())
	require.Contains(t, lastEvent.GetExecutionStalled().GetDescription(), "exceeds")
}
