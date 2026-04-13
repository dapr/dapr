/*
Copyright 2024 The Dapr Authors
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

package monitoring

import (
	"context"
	"sync/atomic"
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	"github.com/dapr/dapr/pkg/diagnostics/utils"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

var (
	sidecarConnectionCount int64 = 0

	sidecarsConnectedGauge = stats.Int64(
		"scheduler/sidecars_connected",
		"The number of dapr sidecars actively connected to the scheduler service.",
		stats.UnitDimensionless)
	jobsScheduledTotal = stats.Int64(
		"scheduler/jobs_created_total",
		"The total number of jobs scheduled.",
		stats.UnitDimensionless)
	jobsTriggeredTotal = stats.Int64(
		"scheduler/jobs_triggered_total",
		"The total number of successfully triggered jobs.",
		stats.UnitDimensionless)
	jobsFailedTotal = stats.Int64(
		"scheduler/jobs_failed_total",
		"The total number of failed jobs.",
		stats.UnitDimensionless)
	jobsUndeliveredTotal = stats.Int64(
		"scheduler/jobs_undelivered_total",
		"The total number of undelivered jobs.",
		stats.UnitDimensionless)
	triggerLatency = stats.Float64(
		"scheduler/trigger_latency",
		"The total time it takes to trigger a job from the scheduler service.",
		stats.UnitMilliseconds)
	concurrencyInflightGauge = stats.Int64(
		"scheduler/concurrency_inflight",
		"Current number of in-flight triggers subject to global concurrency limits on this scheduler instance.",
		stats.UnitDimensionless)
	concurrencyPendingGauge = stats.Int64(
		"scheduler/concurrency_pending",
		"Current number of triggers waiting for a concurrency slot on this scheduler instance.",
		stats.UnitDimensionless)
	concurrencyThrottledTotal = stats.Int64(
		"scheduler/concurrency_throttled_total",
		"Total number of triggers that were throttled by global concurrency limits.",
		stats.UnitDimensionless)
	jobsDeletedTotal = stats.Int64(
		"scheduler/jobs_deleted_total",
		"The total number of deleted jobs.",
		stats.UnitDimensionless)
	jobsBulkDeletedTotal = stats.Int64(
		"scheduler/jobs_bulk_deleted_total",
		"The total number of bulk job deletion operations.",
		stats.UnitDimensionless)
	jobsCreatedFailedTotal = stats.Int64(
		"scheduler/jobs_created_failed_total",
		"The total number of failed job creation attempts.",
		stats.UnitDimensionless)
	sidecarErrorsTotal = stats.Int64(
		"scheduler/sidecar_errors_total",
		"The total number of sidecar connection errors.",
		stats.UnitDimensionless)

	tagType           = tag.MustNewKey("type")
	tagConcurrencyKey = tag.MustNewKey("concurrency_key")
	tagReason         = tag.MustNewKey("reason")
)

var tagSidecarsConnected = utils.WithTags(sidecarsConnectedGauge.Name())

// RecordSidecarsConnectedCount records the number of dapr sidecars connected to the scheduler service
func RecordSidecarsConnectedCount(change int) {
	current := atomic.AddInt64(&sidecarConnectionCount, int64(change))
	stats.RecordWithTags(context.Background(), tagSidecarsConnected, sidecarsConnectedGauge.M(current))
}

var (
	tagTotalJob     = utils.WithTags(jobsScheduledTotal.Name(), tagType, "job")
	tagTotalActor   = utils.WithTags(jobsScheduledTotal.Name(), tagType, "actor")
	tagTotalUnknown = utils.WithTags(jobsScheduledTotal.Name(), tagType, "unknown")
)

// RecordJobsScheduledCount records the number of jobs scheduled to the scheduler service
func RecordJobsScheduledCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagTotalJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagTotalActor
	default:
		tag = tagTotalUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsScheduledTotal.M(1))
}

var (
	tagTriggeredJob     = utils.WithTags(jobsTriggeredTotal.Name(), tagType, "job")
	tagTriggeredActor   = utils.WithTags(jobsTriggeredTotal.Name(), tagType, "actor")
	tagTriggeredUnknown = utils.WithTags(jobsTriggeredTotal.Name(), tagType, "unknown")
)

// RecordJobsTriggeredCount records the total number of jobs successfully triggered from the scheduler service
func RecordJobsTriggeredCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagTriggeredJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagTriggeredActor
	default:
		tag = tagTriggeredUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsTriggeredTotal.M(1))
}

var (
	tagTriggerLatencyJob     = utils.WithTags(triggerLatency.Name(), tagType, "job")
	tagTriggerLatencyActor   = utils.WithTags(triggerLatency.Name(), tagType, "actor")
	tagTriggerLatencyUnknown = utils.WithTags(triggerLatency.Name(), tagType, "unknown")
)

// RecordTriggerDuration records the time it takes to send the job to dapr from the scheduler service
func RecordTriggerDuration(jobMetadata *schedulerv1pb.JobMetadata, duration time.Duration) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagTriggerLatencyJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagTriggerLatencyActor
	default:
		tag = tagTriggerLatencyUnknown
	}
	stats.RecordWithTags(context.Background(), tag, triggerLatency.M(float64(duration.Milliseconds())))
}

var (
	tagFailedJob     = utils.WithTags(jobsFailedTotal.Name(), tagType, "job")
	tagFailedActor   = utils.WithTags(jobsFailedTotal.Name(), tagType, "actor")
	tagFailedUnknown = utils.WithTags(jobsFailedTotal.Name(), tagType, "unknown")
)

// RecordJobsFailed records the total number of failed jobs
func RecordJobsFailedCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagFailedJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagFailedActor
	default:
		tag = tagFailedUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsFailedTotal.M(1))
}

var (
	tagUndeliveredJob     = utils.WithTags(jobsUndeliveredTotal.Name(), tagType, "job")
	tagUndeliveredActor   = utils.WithTags(jobsUndeliveredTotal.Name(), tagType, "actor")
	tagUndeliveredUnknown = utils.WithTags(jobsUndeliveredTotal.Name(), tagType, "unknown")
)

// RecordJobsUndelivered records the total number of undelivered jobs
func RecordJobsUndeliveredCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagUndeliveredJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagUndeliveredActor
	default:
		tag = tagUndeliveredUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsUndeliveredTotal.M(1))
}

// RecordConcurrencyInflight records the current in-flight count for a
// concurrency gate on this scheduler instance.
func RecordConcurrencyInflight(key string, count int64) {
	stats.RecordWithTags(context.Background(),
		utils.WithTags(concurrencyInflightGauge.Name(), tagConcurrencyKey, key),
		concurrencyInflightGauge.M(count),
	)
}

// RecordConcurrencyPending records the current pending count for a
// concurrency gate on this scheduler instance.
func RecordConcurrencyPending(key string, count int64) {
	stats.RecordWithTags(context.Background(),
		utils.WithTags(concurrencyPendingGauge.Name(), tagConcurrencyKey, key),
		concurrencyPendingGauge.M(count),
	)
}

// RecordConcurrencyThrottled records that a trigger was throttled by a
// concurrency gate.
func RecordConcurrencyThrottled(key string) {
	stats.RecordWithTags(context.Background(),
		utils.WithTags(concurrencyThrottledTotal.Name(), tagConcurrencyKey, key),
		concurrencyThrottledTotal.M(1),
	)
}

var (
	tagDeletedJob     = utils.WithTags(jobsDeletedTotal.Name(), tagType, "job")
	tagDeletedActor   = utils.WithTags(jobsDeletedTotal.Name(), tagType, "actor")
	tagDeletedUnknown = utils.WithTags(jobsDeletedTotal.Name(), tagType, "unknown")
)

// RecordJobsDeletedCount records the number of jobs deleted from the scheduler service.
func RecordJobsDeletedCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagDeletedJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagDeletedActor
	default:
		tag = tagDeletedUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsDeletedTotal.M(1))
}

var tagBulkDeleted = utils.WithTags(jobsBulkDeletedTotal.Name())

// RecordJobsBulkDeletedCount records a bulk job deletion operation.
func RecordJobsBulkDeletedCount() {
	stats.RecordWithTags(context.Background(), tagBulkDeleted, jobsBulkDeletedTotal.M(1))
}

var (
	tagCreatedFailedJob     = utils.WithTags(jobsCreatedFailedTotal.Name(), tagType, "job")
	tagCreatedFailedActor   = utils.WithTags(jobsCreatedFailedTotal.Name(), tagType, "actor")
	tagCreatedFailedUnknown = utils.WithTags(jobsCreatedFailedTotal.Name(), tagType, "unknown")
)

// RecordJobsCreatedFailedCount records a failed job creation attempt.
func RecordJobsCreatedFailedCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var tag []tag.Mutator
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		tag = tagCreatedFailedJob
	case *schedulerv1pb.JobTargetMetadata_Actor:
		tag = tagCreatedFailedActor
	default:
		tag = tagCreatedFailedUnknown
	}

	stats.RecordWithTags(context.Background(), tag, jobsCreatedFailedTotal.M(1))
}

// RecordSidecarError records a sidecar connection error with a reason.
func RecordSidecarError(reason string) {
	stats.RecordWithTags(context.Background(),
		utils.WithTags(sidecarErrorsTotal.Name(), tagReason, reason),
		sidecarErrorsTotal.M(1),
	)
}

// InitMetrics initialize the scheduler service metrics.
func InitMetrics() error {
	err := view.Register(
		utils.NewMeasureView(sidecarsConnectedGauge, []tag.Key{}, view.LastValue()),
		utils.NewMeasureView(jobsScheduledTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(jobsTriggeredTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(triggerLatency, []tag.Key{tagType}, view.Distribution(0, 100, 500, 1000, 5000, 10000)),
		utils.NewMeasureView(jobsFailedTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(jobsUndeliveredTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(concurrencyInflightGauge, []tag.Key{tagConcurrencyKey}, view.LastValue()),
		utils.NewMeasureView(concurrencyPendingGauge, []tag.Key{tagConcurrencyKey}, view.LastValue()),
		utils.NewMeasureView(concurrencyThrottledTotal, []tag.Key{tagConcurrencyKey}, view.Count()),
		utils.NewMeasureView(jobsDeletedTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(jobsBulkDeletedTotal, []tag.Key{}, view.Count()),
		utils.NewMeasureView(jobsCreatedFailedTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(sidecarErrorsTotal, []tag.Key{tagReason}, view.Count()),
	)

	return err
}
