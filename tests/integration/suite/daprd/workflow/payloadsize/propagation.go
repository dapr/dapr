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

package payloadsize

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(propagationoversize))
}

type propagationoversize struct {
	workflow *procworkflow.Workflow
}

func (p *propagationoversize) Setup(t *testing.T) []framework.Option {
	p.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithDaprdOptions(0, daprd.WithMaxBodySize("16Mi")),
		procworkflow.WithDaprdOptions(1, daprd.WithMaxBodySize("8Mi")),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *propagationoversize) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	// 4 activities × 2,000,000-byte outputs = ~8 MB of parent history.
	//   - Below parent's threshold (~15.94 MiB) so the parent's own precheck never fires.
	//   - Below the child daprd's gRPC server MaxRecvMsgSize (8Mi = 8,388,608 bytes) so the cross-app schedule succeeds.
	//   - Above the child's workflow precheck threshold (~7,969,177 bytes ≈ 7.6 MiB) so the child stalls.
	const chunkSize = 2_000_000
	const numActivities = 4
	const childInstanceID = "stalled-child"

	chunk := strings.Repeat("x", chunkSize)

	parentReg := p.workflow.RegistryN(0)
	childReg := p.workflow.RegistryN(1)

	parentReg.AddWorkflowN("parent", func(wctx *task.WorkflowContext) (any, error) {
		for range numActivities {
			if err := wctx.CallActivity("emit-chunk").Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, wctx.CallChildWorkflow("child",
			task.WithChildWorkflowAppID(p.workflow.DaprN(1).AppID()),
			task.WithChildWorkflowInstanceID(childInstanceID),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(nil)
	})

	var called atomic.Int32
	parentReg.AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
		called.Add(1)
		return chunk, nil
	})

	var childRan atomic.Bool
	childReg.AddWorkflowN("child", func(wctx *task.WorkflowContext) (any, error) {
		childRan.Store(true)
		return nil, nil
	})

	parentClient := p.workflow.BackendClient(t, ctx)
	childClient := p.workflow.BackendClientN(t, ctx, 1)

	_, err := parentClient.ScheduleNewWorkflow(ctx, "parent")
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, childClient, childInstanceID, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, childClient, childInstanceID)
	require.NotNil(t, stalled)
	require.Equal(t, "PAYLOAD_SIZE_EXCEEDED", stalled.GetExecutionStalled().GetReason().String())
	require.Contains(t, stalled.GetExecutionStalled().GetDescription(), "Workflow payload size")
	require.Contains(t, stalled.GetExecutionStalled().GetDescription(), "increase daprd --max-body-size")

	assert.Equal(t, int32(numActivities), called.Load(), "all activities should have been called successfully in the parent workflow")
	assert.False(t, childRan.Load(), "child workflow should never have run any user code since it stalled on precheck")
}
