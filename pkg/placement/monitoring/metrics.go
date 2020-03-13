// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package monitoring

import (
	"context"

	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"go.opencensus.io/stats"
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

var (
	activeHostsTotal = stats.Int64(
		"placement/hosts_total",
		"The total number of active hosts reported to placement service.",
		stats.UnitDimensionless)
	actorTypesTotal = stats.Int64(
		"placement/actortypes_total",
		"The total number of actor types reported to placement service.",
		stats.UnitDimensionless)
	nonActorHostsTotal = stats.Int64(
		"placement/nonactorhosts_total",
		"The total number of non actor hosts reported to placement service.",
		stats.UnitDimensionless)

	noKeys = []tag.Key{}
)

// RecordActiveHostsCount records the number of active hosts
func RecordActiveHostsCount(count int) {
	stats.Record(context.Background(), activeHostsTotal.M(int64(count)))
}

// RecordActorTypesCount records the number of active actor types
func RecordActorTypesCount(count int) {
	stats.Record(context.Background(), actorTypesTotal.M(int64(count)))
}

// RecordNonActorHostsCount records the number of active non actor hosts
func RecordNonActorHostsCount(count int) {
	stats.Record(context.Background(), nonActorHostsTotal.M(int64(count)))
}

// InitMetrics initialize the placement service metrics
func InitMetrics() error {
	err := view.Register(
		diag_utils.NewMeasureView(activeHostsTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(actorTypesTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(nonActorHostsTotal, noKeys, view.LastValue()),
	)

	return err
}
