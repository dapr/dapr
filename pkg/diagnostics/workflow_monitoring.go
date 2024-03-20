/*
Copyright 2023 The Dapr Authors
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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	workflowNameKey = "workflow_name"
	activityNameKey = "activity_name"
)

const (
	StatusSuccess     = "success"
	StatusFailed      = "failed"
	StatusRecoverable = "recoverable"
	CreateWorkflow    = "create_workflow"
	GetWorkflow       = "get_workflow"
	AddEvent          = "add_event"
	PurgeWorkflow     = "purge_workflow"

	WorkflowEvent = "event"
	Timer         = "timer"
)

type workflowMetrics struct {
	meter metric.Meter

	// workflowOperationCount records count of Successful/Failed requests to Create/Get/Purge Workflow and Add Events.
	workflowOperationCount metric.Int64Counter
	// workflowOperationLatency records latency of response for workflow operation requests.
	workflowOperationLatency metric.Float64Histogram
	// workflowExecutionCount records count of Successful/Failed/Recoverable workflow executions.
	workflowExecutionCount metric.Int64Counter
	// activityExecutionCount records count of Successful/Failed/Recoverable activity executions.
	activityExecutionCount metric.Int64Counter
	// activityExecutionLatency records time taken to run an activity to completion.
	activityExecutionLatency metric.Float64Histogram
	// workflowExecutionLatency records time taken to run a workflow to completion.
	workflowExecutionLatency metric.Float64Histogram
	// workflowSchedulingLatency records time taken between workflow execution request and actual workflow execution
	workflowSchedulingLatency metric.Float64Histogram
	appID                     string
	enabled                   bool
	namespace                 string
}

func newWorkflowMetrics() *workflowMetrics {
	m := otel.Meter("workflow")

	return &workflowMetrics{
		meter:   m,
		enabled: false,
	}
}

func (w *workflowMetrics) IsEnabled() bool {
	return w != nil && w.enabled
}

// Init registers the workflow metrics views.
func (w *workflowMetrics) Init(appID, namespace string) error {
	workflowOperationCount, err := w.meter.Int64Counter(
		"runtime.workflow.operation.count",
		metric.WithDescription("The number of successful/failed workflow operation requests."),
	)
	if err != nil {
		return err
	}

	workflowOperationLatency, err := w.meter.Float64Histogram(
		"runtime.workflow.operation.latency",
		metric.WithDescription("The latencies of responses for workflow operation requests."),
	)
	if err != nil {
		return err
	}

	workflowExecutionCount, err := w.meter.Int64Counter(
		"runtime.workflow.execution.count",
		metric.WithDescription("The number of successful/failed/recoverable workflow executions."),
	)
	if err != nil {
		return err
	}

	activityExecutionCount, err := w.meter.Int64Counter(
		"runtime.workflow.activity.execution.count",
		metric.WithDescription("The number of successful/failed/recoverable activity executions."),
	)
	if err != nil {
		return err
	}

	activityExecutionLatency, err := w.meter.Float64Histogram(
		"runtime.workflow.activity.execution.latency",
		metric.WithDescription("The total time taken to run an activity to completion."),
	)
	if err != nil {
		return err
	}

	workflowExecutionLatency, err := w.meter.Float64Histogram(
		"runtime.workflow.execution.latency",
		metric.WithDescription("The total time taken to run workflow to completion."),
	)
	if err != nil {
		return err
	}

	workflowSchedulingLatency, err := w.meter.Float64Histogram(
		"runtime.workflow.scheduling.latency",
		metric.WithDescription("Interval between workflow execution request and workflow execution."),
	)
	if err != nil {
		return err
	}

	w.activityExecutionCount = activityExecutionCount
	w.activityExecutionLatency = activityExecutionLatency
	w.workflowExecutionCount = workflowExecutionCount
	w.workflowOperationCount = workflowOperationCount
	w.workflowOperationLatency = workflowOperationLatency
	w.workflowExecutionLatency = workflowExecutionLatency
	w.workflowSchedulingLatency = workflowSchedulingLatency
	w.appID = appID
	w.enabled = true
	w.namespace = namespace

	return nil
}

// WorkflowOperationEvent records total number of Successful/Failed workflow Operations requests. It also records latency for those requests.
func (w *workflowMetrics) WorkflowOperationEvent(ctx context.Context, operation, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	w.workflowOperationCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(operationKey, operation), attribute.String(statusKey, status)))

	if elapsed > 0 {
		w.workflowOperationLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(operationKey, operation), attribute.String(statusKey, status)))
	}
}

// WorkflowExecutionEvent records total number of Successful/Failed/Recoverable workflow executions.
// Execution latency for workflow is not supported yet.
func (w *workflowMetrics) WorkflowExecutionEvent(ctx context.Context, workflowName, status string) {
	if !w.IsEnabled() {
		return
	}

	w.workflowExecutionCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(workflowNameKey, workflowName), attribute.String(statusKey, status)))
}

func (w *workflowMetrics) WorkflowExecutionLatency(ctx context.Context, workflowName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		w.workflowExecutionLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(workflowNameKey, workflowName), attribute.String(statusKey, status)))
	}
}

func (w *workflowMetrics) WorkflowSchedulingLatency(ctx context.Context, workflowName string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		w.workflowSchedulingLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(workflowNameKey, workflowName)))
	}
}

// ActivityExecutionEvent records total number of Successful/Failed/Recoverable workflow executions. It also records latency for these executions.
func (w *workflowMetrics) ActivityExecutionEvent(ctx context.Context, activityName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	w.activityExecutionCount.Add(ctx, 1, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(activityNameKey, activityName), attribute.String(statusKey, status)))

	if elapsed > 0 {
		w.activityExecutionLatency.Record(ctx, elapsed, metric.WithAttributes(attribute.String(appIDKey, w.appID), attribute.String(namespaceKey, w.namespace), attribute.String(activityNameKey, activityName), attribute.String(statusKey, status)))
	}
}
