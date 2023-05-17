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

package monitoring

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/tag"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
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
func RecordSidecarInjectionRequestsCount(metrics *diag.Metrics) {
	stats.RecordWithOptions(context.Background(),
		stats.WithRecorder(metrics.Meter),
		stats.WithMeasurements(sidecarInjectionRequestsTotal.M(1)),
	)
}

// RecordSuccessfulSidecarInjectionCount records the number of successful sidecar injections.
func RecordSuccessfulSidecarInjectionCount(metrics *diag.Metrics, appID string) {
	stats.RecordWithOptions(context.Background(),
		metrics.Rules.WithTags(succeededSidecarInjectedTotal.Name(), appIDKey, appID),
		stats.WithMeasurements(succeededSidecarInjectedTotal.M(1)),
	)
}

// RecordFailedSidecarInjectionCount records the number of failed sidecar injections.
func RecordFailedSidecarInjectionCount(metrics *diag.Metrics, appID, reason string) {
	stats.RecordWithOptions(context.Background(),
		metrics.Rules.WithTags(failedSidecarInjectedTotal.Name(), appIDKey, appID, failedReasonKey, reason),
		stats.WithMeasurements(failedSidecarInjectedTotal.M(1)),
	)
}

// InitMetrics initialize the injector service metrics.
func InitMetrics(metrics *diag.Metrics) error {
	return metrics.Meter.Register(
		utils.NewMeasureView(sidecarInjectionRequestsTotal, noKeys, utils.Count()),
		utils.NewMeasureView(succeededSidecarInjectedTotal, []tag.Key{appIDKey}, utils.Count()),
		utils.NewMeasureView(failedSidecarInjectedTotal, []tag.Key{appIDKey, failedReasonKey}, utils.Count()),
	)
}
