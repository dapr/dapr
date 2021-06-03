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
	appID = "app_id"
)

var (
	serviceCreatedTotal = stats.Int64(
		"operator/service_created_total",
		"The total number of dapr services created.",
		stats.UnitDimensionless)
	serviceDeletedTotal = stats.Int64(
		"operator/service_deleted_total",
		"The total number of dapr services deleted.",
		stats.UnitDimensionless)
	serviceUpdatedTotal = stats.Int64(
		"operator/service_updated_total",
		"The total number of dapr services updated.",
		stats.UnitDimensionless)

	// appIDKey is a tag key for App ID.
	appIDKey = tag.MustNewKey(appID)
)

// RecordServiceCreatedCount records the number of dapr service created.
func RecordServiceCreatedCount(appID string) {
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(appIDKey, appID), serviceCreatedTotal.M(1))
}

// RecordServiceDeletedCount records the number of dapr service deleted.
func RecordServiceDeletedCount(appID string) {
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(appIDKey, appID), serviceDeletedTotal.M(1))
}

// RecordServiceUpdatedCount records the number of dapr service updated.
func RecordServiceUpdatedCount(appID string) {
	stats.RecordWithTags(context.Background(), diag_utils.WithTags(appIDKey, appID), serviceUpdatedTotal.M(1))
}

// InitMetrics initialize the operator service metrics.
func InitMetrics() error {
	err := view.Register(
		diag_utils.NewMeasureView(serviceCreatedTotal, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(serviceDeletedTotal, []tag.Key{appIDKey}, view.Count()),
		diag_utils.NewMeasureView(serviceUpdatedTotal, []tag.Key{appIDKey}, view.Count()),
	)

	return err
}
