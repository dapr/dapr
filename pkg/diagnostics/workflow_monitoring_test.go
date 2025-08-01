package diagnostics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opencensus.io/stats/view"

	"github.com/dapr/dapr/pkg/config"
)

func initWorkflowMetrics() (*workflowMetrics, view.Meter) {
	w := newWorkflowMetrics()
	meter := view.NewMeter()
	meter.Start()
	_ = w.Init(meter, "test", "default", config.LoadDefaultConfiguration().GetMetricsSpec().GetLatencyDistribution(log))

	return w, meter
}

func TestOperations(t *testing.T) {
	t.Run("record operation requests", func(t *testing.T) {
		countMetricName := "runtime/workflow/operation/count"
		latencyMetricName := "runtime/workflow/operation/latency"
		t.Run("Create Operation Request", func(t *testing.T) {
			t.Run("Failed Create Operation request count", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), CreateWorkflow, StatusFailed, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Create Operation request count", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), CreateWorkflow, StatusSuccess, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Create Operation request latency", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), CreateWorkflow, StatusSuccess, 1)

				viewData, _ := meter.RetrieveData(latencyMetricName)
				v := meter.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Get Operation Request", func(t *testing.T) {
			t.Run("Failed Get Operation Request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), GetWorkflow, StatusFailed, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Get Operation Request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), GetWorkflow, StatusSuccess, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Get Operation request latency", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), GetWorkflow, StatusSuccess, 1)

				viewData, _ := meter.RetrieveData(latencyMetricName)
				v := meter.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Add Event request", func(t *testing.T) {
			t.Run("Failed Add Event request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), AddEvent, StatusFailed, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Add Event request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), AddEvent, StatusSuccess, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Add Event Operation latency", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), AddEvent, StatusSuccess, 1)

				viewData, _ := meter.RetrieveData(latencyMetricName)
				v := meter.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})

		t.Run("Purge Workflow Request", func(t *testing.T) {
			t.Run("Failed Purge workflow request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), PurgeWorkflow, StatusFailed, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Successful Purge workflow request", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), PurgeWorkflow, StatusSuccess, 0)

				viewData, _ := meter.RetrieveData(countMetricName)
				v := meter.Find(countMetricName)

				allTagsPresent(t, v, viewData[0].Tags)
			})

			t.Run("Purge workflow Operation latency", func(t *testing.T) {
				w, meter := initWorkflowMetrics()
				t.Cleanup(func() { meter.Stop() })

				w.WorkflowOperationEvent(t.Context(), PurgeWorkflow, StatusSuccess, 1)

				viewData, _ := meter.RetrieveData(latencyMetricName)
				v := meter.Find(latencyMetricName)

				allTagsPresent(t, v, viewData[0].Tags)

				assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
			})
		})
	})

	t.Run("record activity operations", func(t *testing.T) {
		countMetricName := "runtime/workflow/activity/execution/count"
		latencyMetricName := "runtime/workflow/activity/execution/latency"
		activityName := "test-activity"
		t.Run("Failed with retryable error", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityExecutionEvent(t.Context(), activityName, StatusRecoverable, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Failed with not-retryable error", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityExecutionEvent(t.Context(), activityName, StatusFailed, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Successful activity execution", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityExecutionEvent(t.Context(), activityName, StatusSuccess, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("activity execution latency", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityExecutionEvent(t.Context(), activityName, StatusSuccess, 1)

			viewData, _ := meter.RetrieveData(latencyMetricName)
			v := meter.Find(latencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(1), viewData[0].Data.(*view.DistributionData).Min, 0)
		})
	})
}

func TestExecution(t *testing.T) {
	t.Run("record activity executions", func(t *testing.T) {
		countMetricName := "runtime/workflow/activity/operation/count"
		latencyMetricName := "runtime/workflow/activity/operation/latency"
		activityName := "test-activity"
		t.Run("Failed with retryable error", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityOperationEvent(t.Context(), activityName, StatusRecoverable, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Failed with not-retryable error", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityOperationEvent(t.Context(), activityName, StatusFailed, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Successful activity request", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityOperationEvent(t.Context(), activityName, StatusSuccess, 0)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("activity operation latency", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.ActivityOperationEvent(t.Context(), activityName, StatusSuccess, 1)

			viewData, _ := meter.RetrieveData(latencyMetricName)
			v := meter.Find(latencyMetricName)

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
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.WorkflowExecutionEvent(t.Context(), workflowName, StatusRecoverable)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Failed with not-retryable error", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.WorkflowExecutionEvent(t.Context(), workflowName, StatusFailed)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("Successful workflow execution", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.WorkflowExecutionEvent(t.Context(), workflowName, StatusSuccess)

			viewData, _ := meter.RetrieveData(countMetricName)
			v := meter.Find(countMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
		})

		t.Run("workflow execution latency", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.WorkflowExecutionLatency(t.Context(), workflowName, StatusSuccess, 20)

			viewData, _ := meter.RetrieveData(executionLatencyMetricName)
			v := meter.Find(executionLatencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(20), viewData[0].Data.(*view.DistributionData).Min, 0)
		})

		t.Run("workflow scheduling latency", func(t *testing.T) {
			w, meter := initWorkflowMetrics()
			t.Cleanup(func() { meter.Stop() })

			w.WorkflowSchedulingLatency(t.Context(), workflowName, 10)

			viewData, _ := meter.RetrieveData(schedulingLatencyMetricName)
			v := meter.Find(schedulingLatencyMetricName)

			allTagsPresent(t, v, viewData[0].Tags)
			assert.InEpsilon(t, float64(10), viewData[0].Data.(*view.DistributionData).Min, 0)
		})
	})
}
