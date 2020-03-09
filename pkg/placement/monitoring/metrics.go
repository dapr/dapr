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
	activeHostsTotal = stats.Int64(
		"placement/hosts_total",
		"The total number of active hosts reported to placement service.",
		stats.UnitDimensionless)
	activeActorTypesTotal = stats.Int64(
		"placement/actortypes_total",
		"The total number of active actor types reported to placement service.",
		stats.UnitDimensionless)
	activeNonActorTypesTotal = stats.Int64(
		"placement/nonactortypes_total",
		"The total number of active non actor types reported to placement service.",
		stats.UnitDimensionless)
)

// RecordActiveHostsCount records the number of active hosts
func RecordActiveHostsCount(count int) {
	stats.Record(context.Background(), activeHostsTotal.M(int64(count)))
}

// RecordActiveActorTypesCount records the number of active actor types
func RecordActiveActorTypesCount(count int) {
	stats.Record(context.Background(), activeActorTypesTotal.M(int64(count)))
}

// RecordActiveNonActorTypesCount records the number of active non actor types
func RecordActiveNonActorTypesCount(count int) {
	stats.Record(context.Background(), activeNonActorTypesTotal.M(int64(count)))
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
		newView(activeHostsTotal, []tag.Key{}, view.LastValue()),
	)

	return err
}
