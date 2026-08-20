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
	taskTypeKey          = tag.MustNewKey("task_type")
	completionRouteKey   = tag.MustNewKey("route")
)

const (
	StatusSuccess = "success"
	StatusFailed  = "failed"
	// Local-wake fast path outcomes beyond success/failed: a failed drive
	// escalated to a durable reminder (or that escalation itself failed,
	// leaving the janitor as the net), and a janitor fire that found and
	// drove a pending inbox (the recovery event; ~0 in healthy steady state).
	StatusEscalated       = "escalated"
	StatusEscalateFailed  = "escalate_failed"
	StatusEscalateSkipped = "escalate_skipped_shutdown"
	// A failed drive against an instance that shows recent life was NOT
	// escalated to a durable reminder: the janitor covers it within one
	// period instead of the scheduler re-driving a merely-slow actor.
	StatusEscalateSuppressed = "escalate_suppressed"
	StatusJanitorRecovered   = "janitor_recovered"
	// A janitor fire found completions held for folding with no live driver
	// (their arming drive was lost and their senders stopped re-delivering,
	// e.g. died with their pod at a placement handoff) and drove a turn to
	// commit them; the rescue event of the captive-fold stranding class
	// (~0 in healthy steady state).
	StatusJanitorFoldRecovered = "janitor_fold_recovered"
	// Local-activity fast path janitor re-dispatch outcomes: an unresolved
	// TaskScheduled event was re-dispatched (the recovery event; ~0 in
	// healthy steady state), found busy executing (benign), or the
	// re-dispatch call failed (the next janitor period retries).
	StatusJanitorRedispatched     = "janitor_redispatched"
	StatusJanitorRedispatchBusy   = "janitor_redispatch_busy"
	StatusJanitorRedispatchFailed = "janitor_redispatch_failed"
	// A janitor re-dispatch was accepted in an earlier period but its task is
	// still unresolved: acceptance only certifies that the target host armed
	// a detached local drive, so the arm is presumed lost and the re-dispatch
	// was escalated to the durable run-activity reminder the fast path elided
	// (a placement-handoff loss window). The durable rescue event; ~0 in
	// healthy steady state.
	StatusJanitorRedispatchEscalated = "janitor_redispatch_escalated"
	// The janitor skipped the re-dispatch check because the instance showed
	// recent progress (fresh durable commit or a running drive loop); the
	// next period re-checks.
	StatusJanitorRedispatchSuppressed = "janitor_redispatch_suppressed"
	// An activity arrival found the in-flight claim held by a dead execution
	// (no engine-held work item after the stale grace) and evicted it so the
	// arrival re-executes; the rescue event of the janitor-livelock class
	// (~0 in healthy steady state).
	StatusClaimEvicted = "claim_evicted"
	// A turn's workflow response re-created an operation that already exists in
	// committed history: the response was computed from older history (a stale
	// or duplicate completion delivery adopted across turns) and the turn was
	// rejected for retry instead of committing a wedged state (the
	// janitor-livelock stranding source; ~0 in healthy steady state).
	StatusStaleTurnRejected = "stale_turn_rejected"
	// Completions-fold outcomes: a sender-retried completion committed
	// inside its folding turn (folded), or was nacked back into the
	// sender's retry chain (turn failure, timeout, deactivation).
	StatusFolded      = "folded"
	StatusFoldNacked  = "fold_nacked"
	StatusTerminated  = "terminated"
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

	// Completion routes under WorkflowsClusteredDeployment. Wait side: the
	// waiter either blocks on the process-local pending map
	// (CompletionRouteWaitLocal, the expected steady state) or falls back to
	// a watch stream on the executor rendezvous actor
	// (CompletionRouteWaitWatch, legacy-format reminders or placement
	// disagreement). Complete side: the completion is either delivered
	// straight into the local pending map of the receiving daprd
	// (CompletionRouteCompleteLocal) or forwarded via the executor actor
	// (CompletionRouteCompleteActor).
	CompletionRouteWaitLocal     = "wait_local"
	CompletionRouteWaitWatch     = "wait_watch"
	CompletionRouteCompleteLocal = "complete_local"
	CompletionRouteCompleteActor = "complete_actor"
)

type workflowMetrics struct {
	// workflowOperationCount records count of Successful/Failed requests to Create/Get/Purge Workflow and Add Events.
	workflowOperationCount *stats.Int64Measure
	// workflowOperationLatency records latency of response for workflow operation requests.
	workflowOperationLatency *stats.Float64Measure
	// workflowExecutionCount records count of Successful/Failed/Terminated/Recoverable workflow executions.
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
	// completionRouteCount records how pending-task completions are routed
	// under WorkflowsClusteredDeployment, tagged by task_type and route.
	// In steady state completions should take the wait_local/complete_*
	// routes; sustained wait_watch indicates broken co-location (e.g.
	// placement churn or legacy-format reminders).
	completionRouteCount *stats.Int64Measure
	// localWakeDriveLatency records the time from a local drive being queued
	// to its turn invocation returning, under the WorkflowsFastPath
	// preview feature. Localizes where fixed-rate latency is spent when the
	// scheduler trigger leg is elided.
	localWakeDriveLatency *stats.Float64Measure
	// localWakeCount records workflow wake-up reminders driven locally under
	// the WorkflowsFastPath preview feature, tagged by status
	// (success = turn ran locally and backstop deletion was attempted;
	// failed = the scheduler backstop drives the turn instead).
	localWakeCount *stats.Int64Measure
	// localActivityDriveLatency records the duration of one locally-driven
	// activity execution attempt (arming to the execution call returning,
	// including the app call), under the WorkflowsFastPath
	// preview feature.
	localActivityDriveLatency *stats.Float64Measure
	// localActivityCount records activity executions driven locally under
	// the WorkflowsFastPath preview feature, tagged by status
	// (success/failed drives, escalations to the durable reminder, and
	// janitor re-dispatch outcomes).
	localActivityCount *stats.Int64Measure
	// completionsFoldCount records sender-retried completions handled by the
	// WorkflowsFastPath preview feature, by outcome.
	completionsFoldCount *stats.Int64Measure
	// completionsFoldWait records how long a folding submit waited for its
	// turn to commit (the sender-visible added latency of the fold).
	completionsFoldWait *stats.Float64Measure
	// lockWaitLatency records the time a workflow orchestrator invocation
	// spends queued on the per-actor turn lock before it starts, tagged by
	// invocation kind (method/reminder/stream). Splits observed invocation
	// latency into lock queueing vs turn body.
	lockWaitLatency *stats.Float64Measure

	// Cached recorders for hot-path records (built in Init): direct
	// meter.Record through prebuilt tag maps instead of the
	// RecordWithOptions allocation chain. One recorder per measure so
	// per-measure metric rules resolve exactly as before.
	workflowOperationCountC   *diagUtils.CachedInt64Counter
	workflowOperationLatencyC *diagUtils.CachedFloat64Recorder
	workflowExecutionCountC   *diagUtils.CachedInt64Counter
	workflowExecutionLatencyC *diagUtils.CachedFloat64Recorder
	schedulingLatencyC        *diagUtils.CachedFloat64Recorder
	activityExecutionCountC   *diagUtils.CachedInt64Counter
	activityExecutionLatencyC *diagUtils.CachedFloat64Recorder
	activityOperationCountC   *diagUtils.CachedInt64Counter
	activityOperationLatencyC *diagUtils.CachedFloat64Recorder

	appID     string
	enabled   bool
	namespace string
	meter     stats.Recorder
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
			"The number of successful/failed/terminated/recoverable workflow executions.",
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
		completionRouteCount: stats.Int64(
			"runtime/workflow/completion/route/count",
			"The number of pending-task completions routed under clustered deployment, by task type and route.",
			stats.UnitDimensionless),
		localWakeCount: stats.Int64(
			"runtime/workflow/local_wake/count",
			"The number of workflow wake-up reminders driven locally by the WorkflowsFastPath preview feature, by status.",
			stats.UnitDimensionless),
		localWakeDriveLatency: stats.Float64(
			"runtime/workflow/local_wake/drive_latency",
			"The latency of locally-driven workflow wake-ups, from queueing the drive to the turn invocation returning.",
			stats.UnitMilliseconds),
		localActivityCount: stats.Int64(
			"runtime/workflow/local_activity/count",
			"The number of activity executions driven locally by the WorkflowsFastPath preview feature, by status.",
			stats.UnitDimensionless),
		localActivityDriveLatency: stats.Float64(
			"runtime/workflow/local_activity/drive_latency",
			"The latency of one locally-driven activity execution attempt, including the app call.",
			stats.UnitMilliseconds),
		completionsFoldCount: stats.Int64(
			"runtime/workflow/completions_fold/count",
			"The number of sender-retried completions handled by the WorkflowsFastPath preview feature, by outcome.",
			stats.UnitDimensionless),
		completionsFoldWait: stats.Float64(
			"runtime/workflow/completions_fold/wait_latency",
			"The time a folding completion submit waited for its turn to commit.",
			stats.UnitMilliseconds),
		lockWaitLatency: stats.Float64(
			"runtime/workflow/lock_wait",
			"The time a workflow orchestrator invocation spends queued on the per-actor turn lock, by invocation kind.",
			stats.UnitMilliseconds),
	}
}

func (w *workflowMetrics) IsEnabled() bool {
	return w != nil && w.enabled
}

// Init registers the workflow metrics views.
func (w *workflowMetrics) Init(meter view.Meter, appID, namespace string, latencyDistribution, workflowLatencyDistribution *view.Aggregation) error {
	w.appID = appID
	w.enabled = true
	w.namespace = namespace
	w.meter = meter

	base := []any{appIDKey, appID, namespaceKey, namespace}
	w.workflowOperationCountC = diagUtils.NewCachedInt64Counter(meter, w.workflowOperationCount, base...)
	w.workflowOperationLatencyC = diagUtils.NewCachedFloat64Recorder(meter, w.workflowOperationLatency, base...)
	w.workflowExecutionCountC = diagUtils.NewCachedInt64Counter(meter, w.workflowExecutionCount, base...)
	w.workflowExecutionLatencyC = diagUtils.NewCachedFloat64Recorder(meter, w.workflowExecutionLatency, base...)
	w.schedulingLatencyC = diagUtils.NewCachedFloat64Recorder(meter, w.workflowSchedulingLatency, base...)
	w.activityExecutionCountC = diagUtils.NewCachedInt64Counter(meter, w.activityExecutionCount, base...)
	w.activityExecutionLatencyC = diagUtils.NewCachedFloat64Recorder(meter, w.activityExecutionLatency, base...)
	w.activityOperationCountC = diagUtils.NewCachedInt64Counter(meter, w.activityOperationCount, base...)
	w.activityOperationLatencyC = diagUtils.NewCachedFloat64Recorder(meter, w.activityOperationLatency, base...)

	err := meter.Register(
		diagUtils.NewMeasureView(w.workflowOperationCount, []tag.Key{appIDKey, namespaceKey, operationKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowOperationLatency, []tag.Key{appIDKey, namespaceKey, operationKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.workflowExecutionCount, []tag.Key{appIDKey, namespaceKey, workflowNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityOperationCount, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityOperationLatency, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.activityExecutionCount, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.activityExecutionLatency, []tag.Key{appIDKey, namespaceKey, activityNameKey, statusKey}, workflowLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowExecutionLatency, []tag.Key{appIDKey, namespaceKey, workflowNameKey, statusKey}, workflowLatencyDistribution),
		diagUtils.NewMeasureView(w.workflowSchedulingLatency, []tag.Key{appIDKey, namespaceKey, workflowNameKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.localWakeDriveLatency, []tag.Key{appIDKey, namespaceKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.attestationGeneratedCount, []tag.Key{appIDKey, namespaceKey, attestationKindKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.attestationVerifiedCount, []tag.Key{appIDKey, namespaceKey, attestationKindKey, attestationResultKey}, view.Count()),
		diagUtils.NewMeasureView(w.attestationVerifyLatency, []tag.Key{appIDKey, namespaceKey, attestationKindKey, attestationResultKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.attestationCertCacheCount, []tag.Key{appIDKey, namespaceKey, certCacheOutcomeKey}, view.Count()),
		diagUtils.NewMeasureView(w.workflowPayloadSizeRatio, []tag.Key{appIDKey, namespaceKey, workflowNameKey}, payloadRatioDistribution),
		diagUtils.NewMeasureView(w.activityPayloadSizeRatio, []tag.Key{appIDKey, namespaceKey, workflowNameKey, activityNameKey}, payloadRatioDistribution),
		diagUtils.NewMeasureView(w.completionRouteCount, []tag.Key{appIDKey, namespaceKey, taskTypeKey, completionRouteKey}, view.Count()),
		// Sum of per-event 1s, not Count: identical exposition (cumulative
		// int64 exports as a Prometheus counter either way), but Sum lets
		// Init pre-record the rescue-evidence statuses at zero below, which
		// Count cannot (a zero record still counts).
		diagUtils.NewMeasureView(w.localWakeCount, []tag.Key{appIDKey, namespaceKey, statusKey}, view.Sum()),
		diagUtils.NewMeasureView(w.localActivityCount, []tag.Key{appIDKey, namespaceKey, statusKey}, view.Sum()),
		diagUtils.NewMeasureView(w.localActivityDriveLatency, []tag.Key{appIDKey, namespaceKey, statusKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.lockWaitLatency, []tag.Key{appIDKey, namespaceKey, operationKey}, latencyDistribution),
		diagUtils.NewMeasureView(w.completionsFoldCount, []tag.Key{appIDKey, namespaceKey, statusKey}, view.Count()),
		diagUtils.NewMeasureView(w.completionsFoldWait, []tag.Key{appIDKey, namespaceKey}, latencyDistribution))
	if err != nil {
		return err
	}

	// Pre-record the rescue-evidence series at zero. They are the
	// ~0-in-healthy-steady-state counters the recovery gates read, and with
	// lazy registration an absent series is indistinguishable from a rescue
	// path that never fired. Their views aggregate by Sum, so the zero
	// record registers the series without changing its value.
	for _, s := range []string{StatusJanitorRecovered, StatusJanitorFoldRecovered, StatusStaleTurnRejected} {
		stats.RecordWithOptions(context.Background(),
			stats.WithRecorder(w.meter),
			stats.WithTags(diagUtils.WithTags(w.localWakeCount.Name(), appIDKey, appID, namespaceKey, namespace, statusKey, s)...),
			stats.WithMeasurements(w.localWakeCount.M(0)))
	}
	for _, s := range []string{StatusJanitorRedispatched, StatusJanitorRedispatchEscalated, StatusClaimEvicted} {
		stats.RecordWithOptions(context.Background(),
			stats.WithRecorder(w.meter),
			stats.WithTags(diagUtils.WithTags(w.localActivityCount.Name(), appIDKey, appID, namespaceKey, namespace, statusKey, s)...),
			stats.WithMeasurements(w.localActivityCount.M(0)))
	}
	return nil
}

// WorkflowOperationEvent records total number of Successful/Failed workflow Operations requests. It also records latency for those requests.
func (w *workflowMetrics) WorkflowOperationEvent(ctx context.Context, operation, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	w.workflowOperationCountC.Record2(operationKey, operation, statusKey, status)

	if elapsed > 0 {
		w.workflowOperationLatencyC.Record2(operationKey, operation, statusKey, status, elapsed)
	}
}

// WorkflowExecutionEvent records total number of Successful/Failed/Terminated/Recoverable workflow executions.
// Execution latency for workflow is not supported yet.
func (w *workflowMetrics) WorkflowExecutionEvent(ctx context.Context, workflowName, status string) {
	if !w.IsEnabled() {
		return
	}

	w.workflowExecutionCountC.Record2(workflowNameKey, workflowName, statusKey, status)
}

func (w *workflowMetrics) WorkflowExecutionLatency(ctx context.Context, workflowName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		w.workflowExecutionLatencyC.Record2(workflowNameKey, workflowName, statusKey, status, elapsed)
	}
}

func (w *workflowMetrics) WorkflowSchedulingLatency(ctx context.Context, workflowName string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	if elapsed > 0 {
		w.schedulingLatencyC.Record1(workflowNameKey, workflowName, elapsed)
	}
}

// ActivityExecutionEvent records total number of Successful/Failed/Recoverable actvity executions. It also records latency for these executions.
func (w *workflowMetrics) ActivityExecutionEvent(ctx context.Context, activityName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	w.activityExecutionCountC.Record2(activityNameKey, activityName, statusKey, status)

	if elapsed > 0 {
		w.activityExecutionLatencyC.Record2(activityNameKey, activityName, statusKey, status, elapsed)
	}
}

// ActivityOperationEvent records total number of Successful/Failed/Recoverable activity requests. It also records latency for these requests.
func (w *workflowMetrics) ActivityOperationEvent(ctx context.Context, activityName, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}

	w.activityOperationCountC.Record2(activityNameKey, activityName, statusKey, status)

	if elapsed > 0 {
		w.activityOperationLatencyC.Record2(activityNameKey, activityName, statusKey, status, elapsed)
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

// WorkflowLocalWakeDrive records the duration of one locally-driven wake
// (queue to turn-invocation return), by outcome status.
func (w *workflowMetrics) WorkflowLocalWakeDrive(ctx context.Context, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.localWakeDriveLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, statusKey, status)...),
		stats.WithMeasurements(w.localWakeDriveLatency.M(elapsed)))
}

// WorkflowLocalWake records a workflow wake-up reminder driven locally under
// the WorkflowsFastPath preview feature, by status.
func (w *workflowMetrics) WorkflowLocalWake(ctx context.Context, status string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.localWakeCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, statusKey, status)...),
		stats.WithMeasurements(w.localWakeCount.M(1)))
}

// WorkflowLocalActivityDrive records the duration of one locally-driven
// activity execution attempt (including the app call), by outcome status.
func (w *workflowMetrics) WorkflowLocalActivityDrive(ctx context.Context, status string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.localActivityDriveLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, statusKey, status)...),
		stats.WithMeasurements(w.localActivityDriveLatency.M(elapsed)))
}

// WorkflowLocalActivity records an activity execution driven locally under
// the WorkflowsFastPath preview feature, by status.
func (w *workflowMetrics) WorkflowLocalActivity(ctx context.Context, status string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.localActivityCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, statusKey, status)...),
		stats.WithMeasurements(w.localActivityCount.M(1)))
}

// WorkflowLockWait records the time an orchestrator invocation spent queued
// on the per-actor turn lock, by invocation kind (method/reminder/stream).
func (w *workflowMetrics) WorkflowLockWait(ctx context.Context, kind string, elapsed float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.lockWaitLatency.Name(), appIDKey, w.appID, namespaceKey, w.namespace, operationKey, kind)...),
		stats.WithMeasurements(w.lockWaitLatency.M(elapsed)))
}

// WorkflowCompletionsFold records a sender-retried completion handled under
// the WorkflowsFastPath preview feature, by outcome.
func (w *workflowMetrics) WorkflowCompletionsFold(ctx context.Context, status string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.completionsFoldCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, statusKey, status)...),
		stats.WithMeasurements(w.completionsFoldCount.M(1)))
}

// WorkflowCompletionsFoldWait records the sender-visible wait of one folding
// completion submit.
func (w *workflowMetrics) WorkflowCompletionsFoldWait(ctx context.Context, elapsed float64) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.completionsFoldWait.Name(), appIDKey, w.appID, namespaceKey, w.namespace)...),
		stats.WithMeasurements(w.completionsFoldWait.M(elapsed)))
}

// WorkflowCompletionRoute records how a pending-task completion was routed
// under WorkflowsClusteredDeployment.
func (w *workflowMetrics) WorkflowCompletionRoute(ctx context.Context, taskType, route string) {
	if !w.IsEnabled() {
		return
	}
	stats.RecordWithOptions(ctx,
		stats.WithRecorder(w.meter),
		stats.WithTags(diagUtils.WithTags(w.completionRouteCount.Name(), appIDKey, w.appID, namespaceKey, w.namespace, taskTypeKey, taskType, completionRouteKey, route)...),
		stats.WithMeasurements(w.completionRouteCount.M(1)))
}
