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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowoversize))
}

// workflowoversize verifies that a workflow whose accumulated history would
// exceed the configured gRPC max body size on the next dispatch is gracefully
// transitioned to STALLED with reason PAYLOAD_SIZE_EXCEEDED rather than
// tearing down the GetWorkItems stream with ResourceExhausted.
type workflowoversize struct {
	workflow *workflow.Workflow
}

func (w *workflowoversize) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("1M"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workflowoversize) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	// Each activity returns a 250 KiB string. After ~4 activities the workflow's
	// combined past history crosses the 95% stall threshold of the 1 MiB max
	// body size, so the next runWorkflow stalls before scheduling.
	const chunkSize = 250 * 1024
	chunk := strings.Repeat("x", chunkSize)

	w.workflow.Registry().AddWorkflowN("growing-history", func(wctx *task.WorkflowContext) (any, error) {
		for range 8 {
			var out string
			if err := wctx.CallActivity("emit-chunk").Await(&out); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	var called atomic.Int32
	w.workflow.Registry().AddActivityN("emit-chunk", func(task.ActivityContext) (any, error) {
		called.Add(1)
		return chunk, nil
	})

	client := w.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "growing-history")
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, stalled)
	require.Equal(t, "PAYLOAD_SIZE_EXCEEDED", stalled.GetExecutionStalled().GetReason().String())
	require.Contains(t, stalled.GetExecutionStalled().GetDescription(), "exceeds")

	assert.Equal(t, int32(4), called.Load())
}
