// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	serviceCreatedTotal = stats.Int64(
		"operator/service_created_total",
		"The total number of dapr service created.",
		stats.UnitDimensionless)
	serviceDeletedTotal = stats.Int64(
		"operator/service_deleted_total",
		"The total number of dapr service deleted.",
		stats.UnitDimensionless)
	serviceUpdatedTotal = stats.Int64(
		"operator/service_updated_total",
		"The total number of dapr service updated.",
		stats.UnitDimensionless)

	noKeys = []tag.Key{}
)

// RecordServiceCreatedCount records the number of dapr service created
func RecordServiceCreatedCount() {
	stats.Record(context.Background(), serviceCreatedTotal.M(1))
}

// RecordServiceDeletedCount records the number of dapr service deleted
func RecordServiceDeletedCount() {
	stats.Record(context.Background(), serviceDeletedTotal.M(1))
}

// RecordServiceUpdatedCount records the number of dapr service updated
func RecordServiceUpdatedCount() {
	stats.Record(context.Background(), serviceUpdatedTotal.M(1))
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
		newView(serviceCreatedTotal, noKeys, view.Count()),
		newView(serviceDeletedTotal, noKeys, view.Count()),
		newView(serviceUpdatedTotal, noKeys, view.Count()),
	)

	return err
}
