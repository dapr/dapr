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
	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"

	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
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
func RecordRuntimesCount(ctx context.Context, count int) {
	stats.Record(ctx, runtimesTotal.M(int64(count)))
}

// RecordActorRuntimesCount records the number of valid actor runtimes.
func RecordActorRuntimesCount(ctx context.Context, count int) {
	stats.Record(ctx, actorRuntimesTotal.M(int64(count)))
}

// InitMetrics initialize the placement service metrics.
func InitMetrics() error {
	err := view.Register(
		diagUtils.NewMeasureView(runtimesTotal, noKeys, view.LastValue()),
		diagUtils.NewMeasureView(actorRuntimesTotal, noKeys, view.LastValue()),
	)

	return err
}
