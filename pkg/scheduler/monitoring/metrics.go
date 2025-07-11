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

	tagType = tag.MustNewKey("type")
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

// InitMetrics initialize the scheduler service metrics.
func InitMetrics() error {
	err := view.Register(
		utils.NewMeasureView(sidecarsConnectedGauge, []tag.Key{}, view.LastValue()),
		utils.NewMeasureView(jobsScheduledTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(jobsTriggeredTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(triggerLatency, []tag.Key{tagType}, view.Distribution(0, 100, 500, 1000, 5000, 10000)),
		utils.NewMeasureView(jobsFailedTotal, []tag.Key{tagType}, view.Count()),
		utils.NewMeasureView(jobsUndeliveredTotal, []tag.Key{tagType}, view.Count()),
	)

	return err
}
