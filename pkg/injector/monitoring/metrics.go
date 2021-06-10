// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(appIDKey, appID), succeededSidecarInjectedTotal.M(1))
}

// RecordFailedSidecarInjectionCount records the number of failed sidecar injections.
func RecordFailedSidecarInjectionCount(appID, reason string) {
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(appIDKey, appID, failedReasonKey, reason), failedSidecarInjectedTotal.M(1))
}

// InitMetrics initialize the injector service metrics.
func InitMetrics() error {
	err := view.Register(
		diag_utils.NewMeasureView(sidecarInjectionRequestsTotal, noKeys, view.Count()),
		diag_utils.NewMeasureView(succeededSidecarInjectedTotal, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(failedSidecarInjectedTotal, []tag.Key{appIDKey, failedReasonKey}, view.Count()),
	)

	return err
}
