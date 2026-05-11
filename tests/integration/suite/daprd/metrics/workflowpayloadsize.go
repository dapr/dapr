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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowPayloadSize))
}

// workflowPayloadSize verifies that runtime/workflow/payload/size_ratio and
// runtime/workflow/activity/payload/size_ratio are populated on a successful
// workflow run when --max-body-size is configured.
type workflowPayloadSize struct {
	workflow *procworkflow.Workflow
}

// 8 MiB matches WithMaxBodySize("8Mi") below.
const maxBodyBytes = 8 * 1024 * 1024

func (w *workflowPayloadSize) Setup(t *testing.T) []framework.Option {
	w.workflow = procworkflow.New(t,
		procworkflow.WithDaprdOptions(0,
			daprd.WithMaxBodySize("8Mi"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workflowPayloadSize) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	const inputSize = 64 * 1024 // 64 KiB → ratio ~= 0.0078 of 8 MiB
	largeInput := strings.Repeat("x", inputSize)

	reg := w.workflow.Registry()
	reg.AddActivityN("echo", func(actx task.ActivityContext) (any, error) {
		var in string
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	})
	reg.AddWorkflowN("payload_size_wf", func(wctx *task.WorkflowContext) (any, error) {
		return nil, wctx.CallActivity("echo", task.WithActivityInput(largeInput)).Await(nil)
	})

	client := w.workflow.BackendClient(t, ctx)
	appID := w.workflow.Dapr().AppID()

	id, err := client.ScheduleNewWorkflow(ctx, "payload_size_wf")
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.True(t, api.WorkflowMetadataIsComplete(meta))

	wfCountKey := "dapr_runtime_workflow_payload_size_ratio_count|app_id:" + appID + "|namespace:|workflow_name:payload_size_wf"
	wfSumKey := "dapr_runtime_workflow_payload_size_ratio_sum|app_id:" + appID + "|namespace:|workflow_name:payload_size_wf"
	actCountKey := "dapr_runtime_workflow_activity_payload_size_ratio_count|activity_name:echo|app_id:" + appID + "|namespace:|workflow_name:payload_size_wf"
	actSumKey := "dapr_runtime_workflow_activity_payload_size_ratio_sum|activity_name:echo|app_id:" + appID + "|namespace:|workflow_name:payload_size_wf"

	wantActivityRatio := float64(inputSize) / float64(maxBodyBytes)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		m := w.workflow.Dapr().Metrics(c, ctx).All()
		// runWorkflow dispatches twice: ExecutionStarted, then TaskCompleted.
		assert.Equal(c, 2, int(m[wfCountKey]), "workflow payload size_ratio sample count")
		assert.Greater(c, m[wfSumKey], float64(0), "workflow payload size_ratio sum")
		// One CallActivity → one activity precheck.
		assert.Equal(c, 1, int(m[actCountKey]), "activity payload size_ratio sample count")
		assert.GreaterOrEqual(c, m[actSumKey], wantActivityRatio, "activity payload size_ratio sum should cover the 64KiB input")
	}, time.Second*10, time.Millisecond*50)
}
