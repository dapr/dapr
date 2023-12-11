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

	WorkflowEvent = "event"
	Timer         = "timer"
)

type workflowMetrics struct {
	// Total Successful and Failed workflow operation requests
	workflowOperationsCount *stats.Int64Measure
	// Workflow operation request's Response latency
	workflowOperationsLatency *stats.Float64Measure
	// Total workflow/activity reminders created
	workflowRemindersCount *stats.Int64Measure
	// Total successful/failed workflow/activity executions
	workflowExecutionCount *stats.Int64Measure
	// Time taken to run a workflow to completion
	workflowExecutionLatency *stats.Float64Measure
	// Latency between execution request and actual execution
	workflowSchedulingLatency *stats.Float64Measure
	appID                     string
	enabled                   bool
	namespace                 string
}

func newWorkflowMetrics() *workflowMetrics {
	return &workflowMetrics{
		workflowOperationsCount: stats.Int64(
			"runtime/workflow/operations/count",
			"The number of successful/failed workflow operation requests.",
			stats.UnitDimensionless),

		workflowOperationsLatency: stats.Float64(
			"runtime/workflow/operations/latency",
			"The latencies of responses for workflow operation requests.",
			stats.UnitMilliseconds),
		workflowRemindersCount: stats.Int64(
			"runtime/workflow/reminders/count",
			"The number of workflow/activity reminders created.",
			stats.UnitDimensionless),
		workflowExecutionCount: stats.Int64(
			"runtime/workflow/execution/count",
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
func (w *workflowMetrics) Init(appID, namespace string) error {
	w.appID = appID
	w.enabled = true
	w.namespace = namespace

	return view.Register(
		diagUtils.NewMeasureView(w.workflowOperationsCount, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationsLatency, []tag.Key{appIDKey, componentKey, namespaceKey, operationKey, statusKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowRemindersCount, []tag.Key{appIDKey, componentKey, namespaceKey, reminderTypeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionCount, []tag.Key{appIDKey, componentKey, namespaceKey, executionTypeKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowExecutionLatency, []tag.Key{appIDKey, componentKey, namespaceKey, executionTypeKey, statusKey}, defaultLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowSchedulingLatency, []tag.Key{appIDKey, componentKey, namespaceKey, executionTypeKey}, defaultLatencyDistribution))
}

// WorkflowOperationsEvent records total number of Successful/Failed workflow Operations requests. It also records latency for those requests.
func (w *workflowMetrics) WorkflowOperationsEvent(ctx context.Context, operation, component, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowOperationsCount.Name(), appIDKey, w.appID, componentKey, component, namespaceKey, w.namespace, operationKey, operation, statusKey, status), w.workflowOperationsCount.M(1))

	if elapsed > 0 {
		stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowOperationsLatency.Name(), appIDKey, w.appID, componentKey, component, namespaceKey, w.namespace, operationKey, operation, statusKey, status), w.workflowOperationsLatency.M(elapsed))
	}

}

// ExecutionEvent records total number of successful/failed workflow/activity executions. It also records latency for executions.
func (w *workflowMetrics) ExecutionEvent(ctx context.Context, component, executionType, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowExecutionCount.Name(), appIDKey, w.appID, componentKey, component, namespaceKey, w.namespace, executionTypeKey, executionType, statusKey, status), w.workflowExecutionCount.M(1))

	if elapsed > 0 {
		stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowExecutionLatency.Name(), appIDKey, w.appID, componentKey, component, namespaceKey, w.namespace, executionTypeKey, executionType, statusKey, status), w.workflowExecutionLatency.M(elapsed))
	}
}

// RemindersTotalCreated records total number of Workflow and Activity reminders created.
func (w *workflowMetrics) RemindersTotalCreated(ctx context.Context, component, reminderType string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithTags(ctx, diagUtils.WithTags(w.workflowRemindersCount.Name(), appIDKey, w.appID, componentKey, component, namespaceKey, w.namespace, reminderTypeKey, reminderType), w.workflowRemindersCount.M(1))
}
