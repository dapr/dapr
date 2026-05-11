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

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

var (
	workflowNameKey      = tag.MustNewKey("workflow_name")
	activityNameKey      = tag.MustNewKey("activity_name")
	attestationKindKey   = tag.MustNewKey("attestation_kind")
	attestationResultKey = tag.MustNewKey("attestation_result")
	certCacheOutcomeKey  = tag.MustNewKey("cert_cache_outcome")
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

	// AttestationKindChild tags attestation events for child workflow
	// completions (ChildCompletionAttestation).
	AttestationKindChild = "child"
	// AttestationKindActivity tags attestation events for activity task
	// completions (ActivityCompletionAttestation).
	AttestationKindActivity = "activity"

	// AttestationResultOK tags an attestation that passed all verification
	// checks at ingestion.
	AttestationResultOK = "ok"
	// AttestationResultReject tags an attestation that was rejected
	// (missing, malformed, tampered, or bound to the wrong parent/task).
	AttestationResultReject = "reject"

	// CertCacheHit tags a lookup that found a cached chain-of-trust
	// verification result within the leaf cert's validity window.
	CertCacheHit = "hit"
	// CertCacheMiss tags a lookup that required a full chain-of-trust
	// verification (first use of this cert digest within the orchestrator
	// instance, or eventTime fell outside the cached window).
	CertCacheMiss = "miss"
)

type workflowMetrics struct {
	// workflowOperationCount records count of Successful/Failed requests to Create/Get/Purge Workflow and Add Events.
	workflowOperationCount *stats.Int64Measure
	// workflowOperationLatency records latency of response for workflow operation requests.
	workflowOperationLatency *stats.Float64Measure
	// workflowExecutionCount records count of Successful/Failed/Recoverable workflow executions.
	workflowExecutionCount *stats.Int64Measure
	// activityOperationCount records count of Successful/Failed requests to create activities.
	activityOperationCount *stats.Int64Measure
	// activityOperationLatency records latency of response for activity operation requests.
	activityOperationLatency *stats.Float64Measure
	// activityExecutionCount records count of Successful/Failed/Recoverable activity executions.
	activityExecutionCount *stats.Int64Measure
	// activityExecutionLatency records time taken to run an activity to completion.
	activityExecutionLatency *stats.Float64Measure
	// workflowExecutionLatency records time taken to run a workflow to completion.
	workflowExecutionLatency *stats.Float64Measure
	// workflowSchedulingLatency records time taken between workflow execution request and actual workflow execution
	workflowSchedulingLatency *stats.Float64Measure
	// attestationGeneratedCount records count of completion attestations
	// produced by this host (child workflow and activity termination paths),
	// tagged by kind (child/activity) and status (success/failed).
	attestationGeneratedCount *stats.Int64Measure
	// attestationVerifiedCount records count of completion attestations
	// verified at inbox ingestion, tagged by kind (child/activity) and
	// result (ok/reject).
	attestationVerifiedCount *stats.Int64Measure
	// attestationVerifyLatency records verification latency per attestation
	// for operators to spot runaway cert-chain verification costs.
	attestationVerifyLatency *stats.Float64Measure
	// attestationCertCacheCount records per-orchestrator cert chain-of-
	// trust cache lookups, tagged by outcome (hit/miss).
	attestationCertCacheCount *stats.Int64Measure
	// workflowPayloadSizeRatio records the serialized size of workflow
	// payloads sent to the SDK as a fraction of the configured gRPC max
	// body size. Operators use this to track how close payloads are to
	// the threshold that triggers a PAYLOAD_SIZE_EXCEEDED stall (~0.95).
	// Only recorded when --max-body-size is configured.
	workflowPayloadSizeRatio *stats.Float64Measure
	// activityPayloadSizeRatio records activity payloads as a fraction of
	// the configured gRPC max body size. Same headroom intent as
	// workflowPayloadSizeRatio.
	activityPayloadSizeRatio *stats.Float64Measure
	appID                    string
	enabled                  bool
	namespace                string
	meter                    stats.Recorder
}

func newWorkflowMetrics() *workflowMetrics {
	return &workflowMetrics{
		workflowOperationCount: stats.Int64(
			"runtime/workflow/operation/count",
			"The number of successful/failed workflow operation requests.",
			stats.UnitDimensionless),
		workflowOperationLatency: stats.Float64(
			"runtime/workflow/operation/latency",
			"The latencies of responses for workflow operation requests.",
			stats.UnitMilliseconds),
		activityOperationCount: stats.Int64(
			"runtime/workflow/activity/operation/count",
			"The number of successful/failed activity operation requests.",
			stats.UnitDimensionless),
		activityOperationLatency: stats.Float64(
			"runtime/workflow/activity/operation/latency",
			"The latencies of responses for activity operation requests.",
			stats.UnitMilliseconds),
		workflowExecutionCount: stats.Int64(
			"runtime/workflow/execution/count",
			"The number of successful/failed/recoverable workflow executions.",
			stats.UnitDimensionless),
		activityExecutionCount: stats.Int64(
			"runtime/workflow/activity/execution/count",
			"The number of successful/failed/recoverable activity executions.",
			stats.UnitDimensionless),
		activityExecutionLatency: stats.Float64(
			"runtime/workflow/activity/execution/latency",
			"The total time taken to run an activity to completion.",
			stats.UnitMilliseconds),
		workflowExecutionLatency: stats.Float64(
			"runtime/workflow/execution/latency",
			"The total time taken to run workflow to completion.",
			stats.UnitMilliseconds),
		workflowSchedulingLatency: stats.Float64(
			"runtime/workflow/scheduling/latency",
			"Interval between workflow execution request and workflow execution.",
			stats.UnitMilliseconds),
		attestationGeneratedCount: stats.Int64(
			"runtime/workflow/attestation/generated/count",
			"The number of completion attestations produced by this host.",
			stats.UnitDimensionless),
		attestationVerifiedCount: stats.Int64(
			"runtime/workflow/attestation/verified/count",
			"The number of completion attestations verified at inbox ingestion.",
			stats.UnitDimensionless),
		attestationVerifyLatency: stats.Float64(
			"runtime/workflow/attestation/verify/latency",
			"The time taken to verify a completion attestation at inbox ingestion.",
			stats.UnitMilliseconds),
		attestationCertCacheCount: stats.Int64(
			"runtime/workflow/attestation/cert_cache/count",
			"The number of per-orchestrator cert chain-of-trust cache lookups, by outcome.",
			stats.UnitDimensionless),
		workflowPayloadSizeRatio: stats.Float64(
			"runtime/workflow/payload/size_ratio",
			"Workflow payload size as a fraction of the configured gRPC max body size; values >=0.95 trip the stall, values >1 exceed the limit.",
			stats.UnitDimensionless),
		activityPayloadSizeRatio: stats.Float64(
			"runtime/workflow/activity/payload/size_ratio",
			"Activity payload size as a fraction of the configured gRPC max body size; values >=0.95 trip the stall, values >1 exceed the limit.",
			stats.UnitDimensionless),
	}
}

func (w *workflowMetrics) IsEnabled() bool {
	return w != nil && w.enabled
}

// Init registers the workflow metrics views.
func (w *workflowMetrics) Init(meter view.Meter, appID, namespace string, latencyDistribution *view.Aggregation) error {
	w.appID = appID
	w.enabled = true
	w.namespace = namespace
	w.meter = meter

	return meter.Register(
		diagUtils.NewMeasureView(w.workflowOperationCount, []tag.Key{appIDKey, namespaceKey, operationKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationLatency, []tag.Key{appIDKey, namespaceKey, operationKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.workflowExecutionCount, []tag.Key{appIDKey, namespaceKey, workflowNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityOperationCount, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityOperationLatency, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.activityExecutionCount, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityExecutionLatency, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.workflowExecutionLatency, []tag.Key{appIDKey, namespaceKey, workflowNameKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.workflowSchedulingLatency, []tag.Key{appIDKey, namespaceKey, workflowNameKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.attestationGeneratedCount, []tag.Key{appIDKey, namespaceKey, attestationKindKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.attestationVerifiedCount, []tag.Key{appIDKey, namespaceKey, attestationKindKey, attestationResultKey}, view.Count()),
		diagUtils.NewMeasureView(w.attestationVerifyLatency, []tag.Key{appIDKey, namespaceKey, attestationKindKey, attestationResultKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.attestationCertCacheCount, []tag.Key{appIDKey, namespaceKey, certCacheOutcomeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowPayloadSizeRatio, []tag.Key{appIDKey, namespaceKey, workflowNameKey}, payloadRatioDistribution),
		diagUtils.NewMeasureView(w.activityPayloadSizeRatio, []tag.Key{appIDKey, namespaceKey, workflowNameKey, activityNameKey}, payloadRatioDistribution))
}

// WorkflowOperationEvent records total number of Successful/Failed workflow Operations requests. It also records latency for those requests.
func (w *workflowMetrics) WorkflowOperationEvent(ctx context.Context, operation, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.workflowOperationCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, operationKey, operation, statusKey, status)...), stats.WithMeasurements(w.workflowOperationCount.M(1)))

	if elapsed > 0 {
		stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.workflowOperationLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, operationKey, operation, statusKey, status)...), stats.WithMeasurements(w.workflowOperationLatency.M(elapsed)))
	}
}

// WorkflowExecutionEvent records total number of Successful/Failed/Recoverable workflow executions.
// Execution latency for workflow is not supported yet.
func (w *workflowMetrics) WorkflowExecutionEvent(ctx context.Context, workflowName, status string) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.workflowExecutionCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, workflowNameKey, workflowName, statusKey, status)...), stats.WithMeasurements(w.workflowExecutionCount.M(1)))
}

func (w *workflowMetrics) WorkflowExecutionLatency(ctx context.Context, workflowName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.workflowExecutionLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, workflowNameKey, workflowName, statusKey, status)...), stats.WithMeasurements(w.workflowExecutionLatency.M(elapsed)))
	}
}

func (w *workflowMetrics) WorkflowSchedulingLatency(ctx context.Context, workflowName string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.workflowSchedulingLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, workflowNameKey, workflowName)...), stats.WithMeasurements(w.workflowSchedulingLatency.M(elapsed)))
	}
}

// ActivityExecutionEvent records total number of Successful/Failed/Recoverable actvity executions. It also records latency for these executions.
func (w *workflowMetrics) ActivityExecutionEvent(ctx context.Context, activityName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.activityExecutionCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, activityNameKey, activityName, statusKey, status)...), stats.WithMeasurements(w.activityExecutionCount.M(1)))

	if elapsed > 0 {
		stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.activityExecutionLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, activityNameKey, activityName, statusKey, status)...), stats.WithMeasurements(w.activityExecutionLatency.M(elapsed)))
	}
}

// ActivityOperationEvent records total number of Successful/Failed/Recoverable activity requests. It also records latency for these requests.
func (w *workflowMetrics) ActivityOperationEvent(ctx context.Context, activityName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.activityOperationCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, activityNameKey, activityName, statusKey, status)...), stats.WithMeasurements(w.activityOperationCount.M(1)))

	if elapsed > 0 {
		stats.RecordWithOptions(ctx, stats.WithRecorder(w.meter), stats.WithTags(diagUtils.WithTags(w.activityOperationLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, activityNameKey, activityName, statusKey, status)...), stats.WithMeasurements(w.activityOperationLatency.M(elapsed)))
	}
}

// AttestationGenerated records a completion attestation being produced
// (either child workflow or activity), tagged by kind and generation
// status. Called from the orchestrator/activity signing paths.
func (w *workflowMetrics) AttestationGenerated(ctx context.Context, kind, status string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.attestationGeneratedCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, attestationKindKey, kind, statusKey, status)...),
		stats.WithMeasurements(w.attestationGeneratedCount.M(1)))
}

// AttestationVerified records an inbox-side verification result, tagged
// by kind and outcome (ok/reject). Also records the time spent verifying.
func (w *workflowMetrics) AttestationVerified(ctx context.Context, kind, result string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.attestationVerifiedCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, attestationKindKey, kind, attestationResultKey, result)...),
		stats.WithMeasurements(w.attestationVerifiedCount.M(1)))
	if elapsed > 0 {
		stats.RecordWithOptions(ctx,
			stats.WithRecorder(w.meter),
			stats.WithTags(diagUtils.WithTags(w.attestationVerifyLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, attestationKindKey, kind, attestationResultKey, result)...),
			stats.WithMeasurements(w.attestationVerifyLatency.M(elapsed)))
	}
}

// AttestationCertCacheLookup records a per-orchestrator cert chain-of-
// trust cache lookup with its outcome (hit/miss).
func (w *workflowMetrics) AttestationCertCacheLookup(ctx context.Context, outcome string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.attestationCertCacheCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, certCacheOutcomeKey, outcome)...),
		stats.WithMeasurements(w.attestationCertCacheCount.M(1)))
}

// WorkflowPayloadSizeRatio records a workflow payload size as a fraction
// of the configured gRPC max body size. Callers should skip recording
// when no max body size is configured (ratio is undefined).
func (w *workflowMetrics) WorkflowPayloadSizeRatio(ctx context.Context, workflowName string, ratio float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.workflowPayloadSizeRatio.Name(), appIDKey, w.appID, namespaceKey, w.namespace, workflowNameKey, workflowName)...),
		stats.WithMeasurements(w.workflowPayloadSizeRatio.M(ratio)))
}

// ActivityPayloadSizeRatio records an activity payload size as a fraction
// of the configured gRPC max body size. Callers should skip recording
// when no max body size is configured.
func (w *workflowMetrics) ActivityPayloadSizeRatio(ctx context.Context, workflowName, activityName string, ratio float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.activityPayloadSizeRatio.Name(), appIDKey, w.appID, namespaceKey, w.namespace, workflowNameKey, workflowName, activityNameKey, activityName)...),
		stats.WithMeasurements(w.activityPayloadSizeRatio.M(ratio)))
}
