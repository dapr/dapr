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

package service

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
)

const (
	appID        = "app_id"
	failedReason = "reason"
)

var (
	sidecarInjectionRequestsTotal = stats.Int64(
		"injector/sidecar_injection/requests_total",
		"The total number of sidecar injection requests.",
		stats.UnitDimensionless)
	succeededSidecarInjectedTotal = stats.Int64(
		"injector/sidecar_injection/succeeded_total",
		"The total number of successful sidecar injections.",
		stats.UnitDimensionless)
	failedSidecarInjectedTotal = stats.Int64(
		"injector/sidecar_injection/failed_total",
		"The total number of failed sidecar injections.",
		stats.UnitDimensionless)

	noKeys = []tag.Key{}

	// appIDKey is a tag key for App ID.
	appIDKey = tag.MustNewKey(appID)

	// failedReasonKey is a tag key for failed reason.
	failedReasonKey = tag.MustNewKey(failedReason)
)

// RecordSidecarInjectionRequestsCount records the total number of sidecar injection requests.
func RecordSidecarInjectionRequestsCount() {
	stats.Record(context.Background(), sidecarInjectionRequestsTotal.M(1))
}

// RecordSuccessfulSidecarInjectionCount records the number of successful sidecar injections.
func RecordSuccessfulSidecarInjectionCount(appID string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(succeededSidecarInjectedTotal.Name(), appIDKey, appID), succeededSidecarInjectedTotal.M(1))
}

// RecordFailedSidecarInjectionCount records the number of failed sidecar injections.
func RecordFailedSidecarInjectionCount(appID, reason string) {
	stats.RecordWithTags(context.Background(), diagUtils.WithTags(failedSidecarInjectedTotal.Name(), appIDKey, appID, failedReasonKey, reason), failedSidecarInjectedTotal.M(1))
}

// InitMetrics initialize the injector service metrics.
func InitMetrics() error {
	err := view.Register(
		diagUtils.NewMeasureView(sidecarInjectionRequestsTotal, noKeys, view.Count()),
		diagUtils.NewMeasureView(succeededSidecarInjectedTotal, []tag.Key{appIDKey}, view.Count()),
		diagUtils.NewMeasureView(failedSidecarInjectedTotal, []tag.Key{appIDKey, failedReasonKey}, view.Count()),
	)

	return err
}
