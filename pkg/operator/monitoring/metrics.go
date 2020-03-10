// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	"github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
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

	// appIDKey is a tag key for App ID
	appIDKey = tag.MustNewKey(appID)
)

// RecordServiceCreatedCount records the number of dapr service created
func RecordServiceCreatedCount(appID string) {
	stats.RecordWithTags(context.Background(), utils.WithTags(appIDKey, appID), serviceCreatedTotal.M(1))
}

// RecordServiceDeletedCount records the number of dapr service deleted
func RecordServiceDeletedCount(appID string) {
	stats.RecordWithTags(context.Background(), utils.WithTags(appIDKey, appID), serviceDeletedTotal.M(1))
}

// RecordServiceUpdatedCount records the number of dapr service updated
func RecordServiceUpdatedCount(appID string) {
	stats.RecordWithTags(context.Background(), utils.WithTags(appIDKey, appID), serviceUpdatedTotal.M(1))
}

func newView(measure stats.Measure, keys []tag.Key, aggregation *view.Aggregation) *view.View {
	return &view.View{
		Name:        measure.Name(),
		Description: measure.Description(),
		Measure:     measure,
		TagKeys:     keys,
		Aggregation: aggregation,
	}
}

// InitMetrics initialize the placement service metrics
func InitMetrics() error {
	err := view.Register(
		newView(serviceCreatedTotal, []tag.Key{appIDKey}, view.Count()),
		newView(serviceDeletedTotal, []tag.Key{appIDKey}, view.Count()),
		newView(serviceUpdatedTotal, []tag.Key{appIDKey}, view.Count()),
	)

	return err
}
