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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/dapr/tests/integration/suite/daprd/metrics/util"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(buckets))
}

// buckets tests daprd metrics buckets for workflows
type buckets struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (b *buckets) Setup(t *testing.T) []framework.Option {
	b.scheduler = scheduler.New(t)

	b.place = placement.New(t)

	app := app.New(t)

	b.daprd = daprd.New(
		t,
		daprd.WithPlacementAddresses(b.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithSchedulerAddresses(b.scheduler.Address()),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowbuckets
spec:
  metrics:
    workflow:
      latencyDistributionBuckets: [1, 5, 10, 30]
      latencyDistributionUnits: 1s
`),
	)

	return []framework.Option{
		framework.WithProcesses(b.place, b.scheduler, app, b.daprd),
	}
}

func (b *buckets) Run(t *testing.T, ctx context.Context) {
	b.scheduler.WaitUntilRunning(t, ctx)
	b.place.WaitUntilRunning(t, ctx)
	b.daprd.WaitUntilRunning(t, ctx)

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
	taskhubClient := client.NewTaskHubGrpcClient(b.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	taskhubClient.StartWorkItemListener(ctx, r)

	t.Run("custom latency buckets", func(t *testing.T) {
		id, err := taskhubClient.ScheduleNewWorkflow(ctx, "workflow", api.WithInput("activity"))
		require.NoError(t, err)
		metadata, err := taskhubClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var workflowLatencyBuckets []float64
		var activityLatencyBuckets []float64
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := b.daprd.Metrics(c, ctx).All()
			workflowLatencyBuckets = collectBuckets(t, metrics, "dapr_runtime_workflow_execution_latency_bucket", "workflow_name:workflow", "status:success")
			activityLatencyBuckets = collectBuckets(t, metrics, "dapr_runtime_workflow_activity_execution_latency_bucket", "activity_name:activity", "status:success")

			assert.NotEmpty(c, workflowLatencyBuckets)
			assert.NotEmpty(c, activityLatencyBuckets)
		}, time.Second*10, time.Millisecond*100)

		expected := []float64{1_000, 5_000, 10_000, 30_000}
		assert.ElementsMatch(t, expected, workflowLatencyBuckets[:len(workflowLatencyBuckets)-1])
		assert.ElementsMatch(t, expected, activityLatencyBuckets[:len(activityLatencyBuckets)-1])
	})
}

func collectBuckets(t *testing.T, metrics map[string]float64, metric, name, status string) []float64 {
	t.Helper()

	var buckets []float64
	for m := range metrics {
		if strings.HasPrefix(m, metric) && strings.Contains(m, name) && strings.Contains(m, status) {
			bucket := util.GetBucketFromKey(t, m)
			buckets = append(buckets, bucket)
		}
	}

	slices.Sort(buckets)

	return buckets
}
