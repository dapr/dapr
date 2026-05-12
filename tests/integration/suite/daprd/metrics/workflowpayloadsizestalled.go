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

package metrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowPayloadSizeStalled))
}

// workflowPayloadSizeStalled verifies that the activity payload size metric
// records the oversize payload at the moment of the PAYLOAD_SIZE_EXCEEDED
// stall, so operators can correlate the size spike with the stall event.
type workflowPayloadSizeStalled struct {
	workflow *procworkflow.Workflow
}

func (w *workflowPayloadSizeStalled) Setup(t *testing.T) []framework.Option {
	w.workflow = procworkflow.New(t,
		procworkflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("8Mi"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workflowPayloadSizeStalled) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	// 8 MiB max-body-size, 95% threshold = 7,969,177 bytes; 8M byte input trips
	// it.
	const inputSize = 8_000_000
	largeInput := strings.Repeat("x", inputSize)

	reg := w.workflow.Registry()
	reg.AddActivityN("echo", func(actx task.ActivityContext) (any, error) {
		var in string
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	})

	reg.AddWorkflowN("stalled_payload_wf", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.CallActivity("echo", task.WithActivityInput("")).Await(nil); err != nil {
			return nil, err
		}
		return nil, wctx.CallActivity("echo", task.WithActivityInput(largeInput)).Await(nil)
	})

	client := w.workflow.BackendClient(t, ctx)
	appID := w.workflow.Dapr().AppID()

	id, err := client.ScheduleNewWorkflow(ctx, "stalled_payload_wf")
	require.NoError(t, err)

	wf.WaitForRuntimeStatus(t, ctx, client, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)

	stalled := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, client, id)
	require.NotNil(t, stalled)
	require.Equal(t, "PAYLOAD_SIZE_EXCEEDED", stalled.GetExecutionStalled().GetReason().String())

	actCountKey := "dapr_runtime_workflow_activity_payload_size_ratio_count|activity_name:echo|app_id:" + appID + "|namespace:|workflow_name:stalled_payload_wf"
	actSumKey := "dapr_runtime_workflow_activity_payload_size_ratio_sum|activity_name:echo|app_id:" + appID + "|namespace:|workflow_name:stalled_payload_wf"
	wfCountKey := "dapr_runtime_workflow_payload_size_ratio_count|app_id:" + appID + "|namespace:|workflow_name:stalled_payload_wf"

	// Lower bound on activity ratio sum: empty-input ratio (~0) + oversize ratio
	// (>= inputSize / maxBodyBytes). The oversize sample is the dominant term.
	wantActivityRatioSum := float64(inputSize) / float64(maxBodyBytes)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		m := w.workflow.Dapr().Metrics(c, ctx).All()
		assert.Equal(c, 2, int(m[wfCountKey]), "workflow payload size_ratio sample count")
		assert.Equal(c, 2, int(m[actCountKey]), "activity payload size_ratio sample count")
		assert.GreaterOrEqual(c, m[actSumKey], wantActivityRatioSum, "activity payload size_ratio sum should reflect the oversize payload")
	}, time.Second*10, time.Millisecond*10)
}
