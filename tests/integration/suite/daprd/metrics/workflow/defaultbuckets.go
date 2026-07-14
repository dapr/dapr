/*
Copyright 2024 The Dapr Authors
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/metrics/util"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(defaultBuckets))
}

// defaultBuckets tests daprd default metrics buckets for workflows
type defaultBuckets struct {
	w *workflow.Workflow
}

func (b *defaultBuckets) Setup(t *testing.T) []framework.Option {
	b.w = workflow.New(
		t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(b.w),
	}
}

func (b *defaultBuckets) Run(t *testing.T, ctx context.Context) {
	b.w.WaitUntilRunning(t, ctx)

	// Register workflow
	r := task.NewTaskRegistry()
	r.AddActivityN("activity", func(ctx task.ActivityContext) (any, error) {
		return "success", nil
	})
	r.AddWorkflowN("workflow", func(ctx *task.WorkflowContext) (any, error) {
		err := ctx.CallActivity("activity").Await(nil)
		if err != nil {
			return nil, err
		}
		return nil, nil
	})
	client := client.NewTaskHubGrpcClient(b.w.Dapr().GRPCConn(t, ctx), backend.DefaultLogger())
	client.StartWorkItemListener(ctx, r)

	t.Run("default latency buckets", func(t *testing.T) {
		id, err := client.ScheduleNewWorkflow(ctx, "workflow", api.WithInput("activity"))
		require.NoError(t, err)
		metadata, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var workflowLatencyBuckets []float64
		var activityLatencyBuckets []float64
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := b.w.Metrics(t, ctx)
			workflowLatencyBuckets = util.CollectBuckets(t, metrics, "dapr_runtime_workflow_execution_latency_bucket", "workflow_name:workflow", "status:success")
			activityLatencyBuckets = util.CollectBuckets(t, metrics, "dapr_runtime_workflow_activity_execution_latency_bucket", "activity_name:activity", "status:success")

			assert.NotEmpty(c, workflowLatencyBuckets)
			assert.NotEmpty(c, activityLatencyBuckets)
		}, time.Second*10, time.Millisecond*100)

		// default copied from pkg/config/configuration.go:277
		expected := []float64{1, 2, 3, 4, 5, 6, 8, 10, 13, 16, 20, 25, 30, 40, 50, 65, 80, 100, 130, 160, 200, 250, 300, 400, 500, 650, 800, 1_000, 2_000, 5_000, 10_000, 20_000, 50_000, 100_000}
		assert.ElementsMatch(t, expected, workflowLatencyBuckets[:len(workflowLatencyBuckets)-1])
		assert.ElementsMatch(t, expected, activityLatencyBuckets[:len(activityLatencyBuckets)-1])
	})
}
