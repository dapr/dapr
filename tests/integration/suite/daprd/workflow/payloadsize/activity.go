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
	suite.Register(new(activityoversize))
}

type activityoversize struct {
	workflow *workflow.Workflow
}

func (a *activityoversize) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("8Mi"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activityoversize) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// 8 MiB max-body-size = 8,388,608 bytes; 95% threshold = 7,969,177 bytes.
	const inputSize = 8_000_000
	largeInput := strings.Repeat("x", inputSize)

	a.workflow.Registry().AddWorkflowN("calls-large-activity", func(wctx *task.WorkflowContext) (any, error) {
		wctx.CallActivity("echo", task.WithActivityInput("")).Await(nil)
		return nil, wctx.CallActivity("echo", task.WithActivityInput(largeInput)).Await(nil)
	})

	var called atomic.Int32
	a.workflow.Registry().AddActivityN("echo", func(actx task.ActivityContext) (any, error) {
		called.Add(1)
		var in string
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	})

	client := a.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "calls-large-activity")
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, stalled)
	require.Equal(t, "PAYLOAD_SIZE_EXCEEDED", stalled.GetExecutionStalled().GetReason().String())
	require.Contains(t, stalled.GetExecutionStalled().GetDescription(), "activity")
	require.Contains(t, stalled.GetExecutionStalled().GetDescription(), "increase daprd --max-body-size")

	assert.Equal(t, int32(1), called.Load(), "expected the first activity call to succeed and the second to be rejected")
}
