package diagnostics

import (
	"context"
	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"
	"testing"
)

const (
	workflowName = "test"
)

// Not sure about value for "ComponentKey", so passing "test" as component, through "workflowName" variable.

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

				w.WorkflowOperationsEvent(context.Background(), CreateWorkflow, workflowName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Create Operation request count", func(t *testing.T) {
				w := workflowMetrics{}

				w.WorkflowOperationsEvent(context.Background(), CreateWorkflow, workflowName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Create Operation request latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), CreateWorkflow, workflowName, StatusSuccess, 2)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(2), viewData[0].Data.(*view.DistributionData).Min)

			})
		})

		t.Run("Get Operation Request", func(t *testing.T) {
			t.Run("Failed Get Operation Request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), GetWorkflow, workflowName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Get Operation Request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), GetWorkflow, workflowName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Get Operation request latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), GetWorkflow, workflowName, StatusSuccess, 1)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(1), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Add Event request", func(t *testing.T) {
			t.Run("Failed Add Event request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), AddEvent, workflowName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Add Event request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), AddEvent, workflowName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Add Event Operation latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), AddEvent, workflowName, StatusSuccess, 2)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(2), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Purge Workflow Request", func(t *testing.T) {
			t.Run("Failed Purge workflow request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), PurgeWorkflow, workflowName, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Purge workflow request", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), PurgeWorkflow, workflowName, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Purge workflow Operation latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.WorkflowOperationsEvent(context.Background(), PurgeWorkflow, workflowName, StatusSuccess, 2)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.Equal(t, float64(2), viewData[0].Data.(*view.DistributionData).Min)
			})
		})
	})
}

func TestReminders(t *testing.T) {
	t.Run("record reminder creation", func(t *testing.T) {
		countMetricName := "runtime/workflow/reminders/count"

		t.Run("Activity reminder", func(t *testing.T) {
			w := workflowsMetrics()

			w.RemindersTotalCreated(context.Background(), workflowName, Activity)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Workflow reminder", func(t *testing.T) {
			w := workflowsMetrics()

			w.RemindersTotalCreated(context.Background(), workflowName, Workflow)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Event reminder", func(t *testing.T) {
			w := workflowsMetrics()

			w.RemindersTotalCreated(context.Background(), workflowName, WorkflowEvent)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Timer reminder", func(t *testing.T) {
			w := workflowsMetrics()

			w.RemindersTotalCreated(context.Background(), workflowName, Timer)

			viewData, _ := view.RetrieveData(countMetricName)
			v := view.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
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

				w.ExecutionEvent(context.Background(), workflowName, Activity, StatusRetryable, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Failed with not-retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Activity, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

			})

			t.Run("Successful activity execution", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Activity, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("activity execution latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Activity, StatusSuccess, 4)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
				assert.Equal(t, float64(2), viewData[0].Data.(*view.DistributionData).Min)
			})
		})

		t.Run("Workflow executions", func(t *testing.T) {
			t.Run("Failed with retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Workflow, StatusRetryable, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Failed with not-retryable error", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Workflow, StatusFailed, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

			})

			t.Run("Successful activity execution", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Workflow, StatusSuccess, 0)

				viewData, _ := view.RetrieveData(countMetricName)
				v := view.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("activity execution latency", func(t *testing.T) {
				w := workflowsMetrics()

				w.ExecutionEvent(context.Background(), workflowName, Workflow, StatusSuccess, 4)

				viewData, _ := view.RetrieveData(latencyMetricName)
				v := view.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
				assert.Equal(t, float64(2), viewData[0].Data.(*view.DistributionData).Min)
			})
		})
	})
}
