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
)

const (
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusRetryable = "retryable"
	CreateWorkflow  = "create_workflow"
	GetWorkflow     = "get_workflow"
	AddEvent        = "add_event"
	PurgeWorkflow   = "purge_workflow"
	Activity        = "activity"
	Workflow        = "workflow"
	WorkflowEvent   = "event"
	Timer           = "timer"
)

type workflowMetrics struct {
	// Total Successful and Failed workflow operation requests
	workflowOperationsCount *stats.Int64Measure
	// Workflow operation request's Response latency
	workflowOperationsLatency *stats.Float64Measure
	// Total workflow/activity reminders created
	workflowRemindersTotal *stats.Int64Measure
	// Total successful/failed workflow/activity executions
	workflowExecutionCount *stats.Int64Measure
	// Time taken to run a workflow to completion
	workflowExecutionLatency *stats.Float64Measure
	// Latency between execution request and actual execution
	workflowSchedulingLatency *stats.Float64Measure
	appID                     string
	enabled                   bool
}

func newWorkflowMetrics() *workflowMetrics {
	return &workflowMetrics{
		workflowOperationsCount: stats.Int64(
			"runtime/workflow/operations/total",
			"The number of successful/failed workflow operation requests.",
			stats.UnitDimensionless),

		workflowOperationsLatency: stats.Float64(
			"runtime/workflow/operations/latency",
			"The latencies of responses for workflow operation requests.",
			stats.UnitMilliseconds),
		workflowRemindersTotal: stats.Int64(
			"runtime/workflows/reminders/total",
			"The number of workflows/activity reminders created.",
			stats.UnitDimensionless),
		workflowExecutionCount: stats.Int64(
			"runtime/workflow/execution/total",
			"The number of successful/failed workflow/activity executions.",
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
		diagUtils.NewMeasureView(w.workflowOperationsCount, []tag.Key{appIDKey, operationKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationsLatency, []tag.Key{appIDKey, elapsedKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowRemindersTotal, []tag.Key{appIDKey, reminderTypeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionCount, []tag.Key{appIDKey, executionTypeKey, statusKey}, view.Count()),
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

// ExecutionCompleted records total number of successful/failed workflow/activity executions.
func (w *workflowMetrics) ExecutionCount(ctx context.Context, executionType, status string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowExecutionCount.Name(), appIDKey, w.appID, executionTypeKey, executionType, statusKey, status))
}

// WorkflowOperationsCount records total number of Successful/Failed workflow Operations requests.
func (w *workflowMetrics) WorkflowOperationsCount(ctx context.Context, operation, status string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowOperationsCount.Name(), appIDKey, w.appID, operationKey, operation, statusKey, status), w.workflowOperationsCount.M(1))

}

// TODO: Implementing logic to record latencies.
