package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
)

func workflowsMetrics() *workflowMetrics {
	w := newWorkflowMetrics()
	w.Init("test", "default")

	return w
}

func TestOperations(t *testing.T) {
	t.Run("record operation requests", func(t *testing.T) {
		countMetricName := "runtime/workflow/operations/count"
		latencyMetricName := "runtime/workflow/operations/latency"
		t.Run("Create Operation Request", func(t *testing.T) {
			t.Run("Failed Create Operation request count", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, componentName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Create Operation request count", func(t *testing.T) {
				w := workflowMetrics{}

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, componentName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Create Operation request latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, componentName, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)

			})
		})

		t.Run("Get Operation Request", func(t *testing.T) {
			t.Run("Failed Get Operation Request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, componentName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Get Operation Request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, componentName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Get Operation request latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, componentName, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Add Event request", func(t *testing.T) {
			t.Run("Failed Add Event request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, componentName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Add Event request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, componentName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Add Event Operation latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, componentName, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Purge Workflow Request", func(t *testing.T) {
			t.Run("Failed Purge workflow request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, componentName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Purge workflow request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, componentName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Purge workflow Operation latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, componentName, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})
	})
}

func TestExecution(t *testing.T) {
	t.Run("record execution metrics", func(t *testing.T) {
		countMetricName := "runtime/workflow/execution/count"
		latencyMetricName := "runtime/workflow/execution/latency"
		t.Run("Activity executions", func(t *testing.T) {
			t.Run("Failed with retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Activity, StatusRecoverable, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Failed with not-retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Activity, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

			})

			t.Run("Successful activity execution", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Activity, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("activity execution latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Activity, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Workflow executions", func(t *testing.T) {
			t.Run("Failed with retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Workflow, StatusRecoverable, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Failed with not-retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Workflow, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

			})

			t.Run("Successful activity execution", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Workflow, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("activity execution latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), componentName, Workflow, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})
	})
}
