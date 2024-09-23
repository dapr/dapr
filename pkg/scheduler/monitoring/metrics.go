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
	"time"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

const (
	appID = "app_id"
)

var (
	sidecarsConnectedTotal = stats.Int64(
		"scheduler/sidecars_connected_total",
		"The total number of dapr sidecars connected to the scheduler service.",
		stats.UnitDimensionless)
	jobsScheduledTotal = stats.Int64(
		"scheduler/jobs_created_total",
		"The total number of jobs scheduled.",
		stats.UnitDimensionless)
	jobsTriggeredTotal = stats.Int64(
		"scheduler/jobs_triggered_total",
		"The total number of successfully triggered jobs.",
		stats.UnitDimensionless)
	triggerDurationTotal = stats.Float64(
		"scheduler/trigger_duration_total",
		"The total time it takes to trigger a job from the scheduler service.",
		stats.UnitMilliseconds)

	// appIDKey is a tag key for App ID.
	appIDKey = tag.MustNewKey(appID)
)

// RecordSidecarsConnectedCount records the number of dapr sidecars connected to the scheduler service
func RecordSidecarsConnectedCount(ns string, appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(sidecarsConnectedTotal.Name(), appIDKey, ns, appID), sidecarsConnectedTotal.M(1))
}

// RecordJobsScheduledCount records the number of jobs scheduled to the scheduler service
func RecordJobsScheduledCount(jobMetadata *schedulerv1pb.JobMetadata) {
	var jobType string
	switch jobMetadata.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		jobType = "job"
	case *schedulerv1pb.JobTargetMetadata_Actor:
		jobType = "actor"
	default:
		jobType = "unknown"
	}

	stats.RecordWithTags(context.Background(), diagUtils.WithTags(jobsScheduledTotal.Name(), appIDKey, jobMetadata.GetAppId(), jobType), jobsScheduledTotal.M(1))
}

// RecordJobsTriggeredCount records the total number of jobs successfully triggered from the scheduler service
func RecordJobsTriggeredCount(ns string, appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(jobsTriggeredTotal.Name(), appIDKey, ns, appID), jobsTriggeredTotal.M(1))
}

// RecordTriggerDuration records the time it takes to send the job to dapr from the scheduler service
func RecordTriggerDuration(ns string, appID string, start time.Time) {
	elapsed := time.Since(start).Milliseconds()
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(triggerDurationTotal.Name(), appIDKey, ns, appID), triggerDurationTotal.M(float64(elapsed)))
}

// InitMetrics initialize the scheduler service metrics.
func InitMetrics() error {
	err := view.Register(
		diagUtils.NewMeasureView(sidecarsConnectedTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(jobsScheduledTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(jobsTriggeredTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(triggerDurationTotal, []tag.Key{appIDKey}, view.Distribution(0, 100, 500, 1000, 5000, 10000)),
	)

	return err
}
