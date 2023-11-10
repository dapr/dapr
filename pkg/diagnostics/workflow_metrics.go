/*
Copyright 2021 The Dapr Authors
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

package diagnostics

import (
	"context"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	elapsedKey = tag.MustNewKey("elapsed")

	reminderTypeKey = tag.MustNewKey("reminder_type")

	executionTypeKey = tag.MustNewKey("execution_type")

	isRetryableKey = tag.MustNewKey("is_retryable")
)

type workflowMetrics struct {
	// Total successful workflow operation requests
	workflowOperationsTotal *stats.Int64Measure
	// Total failed workflow operation requests
	workflowOperationsFailedTotal *stats.Int64Measure
	// Workflow operation request's Response latency
	workflowOperationsLatency *stats.Float64Measure

	// Total workflow/activity reminders created
	workflowRemindersTotal *stats.Int64Measure

	// Total successful workflow/activity executions
	workflowExecutionTotal *stats.Int64Measure
	// Total failed workflow/activity executions
	workflowExecutionFailedTotal *stats.Int64Measure
	// Time taken to run a workflow to completion
	workflowExecutionLatency *stats.Float64Measure

	// Latency between execution request and actual execution
	workflowSchedulingLatency *stats.Float64Measure

	appID   string
	enabled bool
}

func newWorkflowMetrics() *workflowMetrics {
	return &workflowMetrics{
		workflowOperationsTotal: stats.Int64(
			"runtime/workflow/operations/total",
			"The number of successful workflow operation requests.",
			stats.UnitDimensionless),

		workflowOperationsFailedTotal: stats.Int64(
			"runtime/workflow/operations/failed_total",
			"The number of failed workflow operations requests.",
			stats.UnitDimensionless),
		workflowOperationsLatency: stats.Float64(
			"runtime/workflow/operations/latency",
			"The latencies of responses for workflow operation requests.",
			stats.UnitMilliseconds),
		workflowRemindersTotal: stats.Int64(
			"runtime/workflows/reminders/total",
			"The number of workflows/activity reminders created.",
			stats.UnitDimensionless),
		workflowExecutionTotal: stats.Int64(
			"runtime/workflow/execution/total",
			"The number of successful workflow/activity executions.",
			stats.UnitDimensionless),
		workflowExecutionFailedTotal: stats.Int64(
			"runtime/workflow/execution/failed_total",
			"The number of failed workflow/activity executions.",
			stats.UnitDimensionless),
		workflowExecutionLatency: stats.Float64(
			"runtime/workflow/execution/latency",
			"The total time taken to run a workflow/activity to completion.",
			stats.UnitMilliseconds),
		workflowSchedulingLatency: stats.Float64(
			"runtime/workflow/scheduling/latency",
			"The latency between execution request and actual execution.",
			stats.UnitMilliseconds),
	}
}

func (w *workflowMetrics) IsEnabled() bool {
	return w != nil && w.enabled
}

// Init registers the workflow metrics views.
func (w *workflowMetrics) Init(appID string) error {
	w.appID = appID
	w.enabled = true

	return view.Register(
		diagUtils.NewMeasureView(w.workflowOperationsTotal, []tag.Key{appIDKey, operationKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationsFailedTotal, []tag.Key{appIDKey, operationKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationsLatency, []tag.Key{appIDKey, elapsedKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowRemindersTotal, []tag.Key{appIDKey, reminderTypeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionTotal, []tag.Key{appIDKey, executionTypeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionFailedTotal, []tag.Key{appIDKey, executionTypeKey, isRetryableKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionLatency, []tag.Key{appIDKey, executionTypeKey, statusKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowSchedulingLatency, []tag.Key{appIDKey, executionTypeKey}, defaultLatencyDistribution))
}

// RemindersTotalCreated records total number of Workflow and Activity reminders created.
func (w *workflowMetrics) RemindersTotalCreated(ctx context.Context, reminderType string) {
	if !w.IsEnabled() {
		return
	}
	// [Question] Why are we ignoring errors in recording metrics ?
	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowRemindersTotal.Name(), appIDKey, w.appID, reminderTypeKey, reminderType), w.workflowRemindersTotal.M(1))
}

// ExecutionCompleted records total number of successful workflow/activity executions.
func (w *workflowMetrics) ExecutionCompleted(ctx context.Context, executionType string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowExecutionTotal.Name(), appIDKey, w.appID, executionTypeKey, executionType), w.workflowExecutionTotal.M(1))
}

// ExecutionFailed records total number of failed workflow/activity executions.
func (w *workflowMetrics) ExecutionFailed(ctx context.Context, executionType string, isRetryable bool) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowExecutionFailedTotal.Name(), appIDKey, w.appID, executionTypeKey, executionType, isRetryableKey, isRetryable), w.workflowExecutionFailedTotal.M(1))
}

// WorkflowOperationsSuccessful records total number of successful workflow Operations requests.
func (w *workflowMetrics) WorkflowOperationsSuccessful(ctx context.Context, operation string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowOperationsTotal.Name(), appIDKey, w.appID, operationKey, operation), w.workflowOperationsTotal.M(1))
}

// WorkflowOperationsFailed records total number of failed workflow Operations requests.
func (w *workflowMetrics) WorkflowOperationsFailed(ctx context.Context, operation string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowOperationsFailedTotal.Name(), appIDKey, w.appID, operationKey, operation), w.workflowOperationsFailedTotal.M(1))
}

// TODO: Implementing logic to record latencies.
