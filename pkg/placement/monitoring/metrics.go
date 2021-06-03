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

var (
	runtimesTotal = stats.Int64(
		"placement/runtimes_total",
		"The total number of runtimes reported to placement service.",
		stats.UnitDimensionless)
	actorRuntimesTotal = stats.Int64(
		"placement/actor_runtimes_total",
		"The total number of actor runtimes reported to placement service.",
		stats.UnitDimensionless)

	noKeys = []tag.Key{}
)

// RecordRuntimesCount records the number of connected runtimes.
func RecordRuntimesCount(count int) {
	stats.Record(context.Background(), runtimesTotal.M(int64(count)))
}

// RecordActorRuntimesCount records the number of valid actor runtimes.
func RecordActorRuntimesCount(count int) {
	stats.Record(context.Background(), actorRuntimesTotal.M(int64(count)))
}

// InitMetrics initialize the placement service metrics.
func InitMetrics() error {
	err := view.Register(
		diag_utils.NewMeasureView(runtimesTotal, noKeys, view.LastValue()),
		diag_utils.NewMeasureView(actorRuntimesTotal, noKeys, view.LastValue()),
	)

	return err
}
