package diagnostics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
)

func initWorkflowMetrics() *workflowMetrics {
	w := newWorkflowMetrics()
	w.Init("test", "default")

	return w
}

func TestOperations(t *testing.T) {
	t.Run("record operation requests", func(t *testing.T) {
		countMetricName := "runtime/workflow/operation/count"
		latencyMetricName := "runtime/workflow/operation/latency"
		t.Run("Create Operation Request", func(t *testing.T) {
			t.Run("Failed Create Operation request count", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Create Operation request count", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Create Operation request latency", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), CreateWorkflow, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Get Operation Request", func(t *testing.T) {
			t.Run("Failed Get Operation Request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Get Operation Request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Get Operation request latency", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), GetWorkflow, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Add Event request", func(t *testing.T) {
			t.Run("Failed Add Event request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Add Event request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Add Event Operation latency", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), AddEvent, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Purge Workflow Request", func(t *testing.T) {
			t.Run("Failed Purge workflow request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Purge workflow request", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Purge workflow Operation latency", func(t *testing.T) {
				w := initWorkflowMetrics()

				w.WorkflowOperationEvent(context.Background(), PurgeWorkflow, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})
	})
}

func TestExecution(t *testing.T) {
	t.Run("record activity executions", func(t *testing.T) {
		countMetricName := "runtime/workflow/activity/execution/count"
		latencyMetricName := "runtime/workflow/activity/execution/latency"
		activityName := "test-activity"
		t.Run("Failed with retryable error", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.ActivityExecutionEvent(context.Background(), activityName, StatusRecoverable, 0)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Failed with not-retryable error", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.ActivityExecutionEvent(context.Background(), activityName, StatusFailed, 0)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Successful activity execution", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.ActivityExecutionEvent(context.Background(), activityName, StatusSuccess, 0)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("activity execution latency", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.ActivityExecutionEvent(context.Background(), activityName, StatusSuccess, 1)

			viewData, _ := view.RetrieveData(latencyMetricName)
			v := view.Find(latencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
		})
	})

	t.Run("record workflow executions", func(t *testing.T) {
		countMetricName := "runtime/workflow/execution/count"
		executionLatencyMetricName := "runtime/workflow/execution/latency"
		schedulingLatencyMetricName := "runtime/workflow/scheduling/latency"
		workflowName := "test-workflow"

		t.Run("Failed with retryable error", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.WorkflowExecutionEvent(context.Background(), workflowName, StatusRecoverable)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Failed with not-retryable error", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.WorkflowExecutionEvent(context.Background(), workflowName, StatusFailed)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Successful workflow execution", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.WorkflowExecutionEvent(context.Background(), workflowName, StatusSuccess)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("workflow execution latency", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.WorkflowExecutionLatency(context.Background(), workflowName, StatusSuccess, 20)

			viewData, _ := view.RetrieveData(executionLatencyMetricName)
			v := view.Find(executionLatencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(20), viewData[0].Data.(*view.DistributionData).Min, 0)
		})

		t.Run("workflow scheduling latency", func(t *testing.T) {
			w := initWorkflowMetrics()

			w.WorkflowSchedulingLatency(context.Background(), workflowName, 10)

			viewData, _ := view.RetrieveData(schedulingLatencyMetricName)
			v := view.Find(schedulingLatencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(10), viewData[0].Data.(*view.DistributionData).Min, 0)
		})
	})
}
