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

package diagnostics

import (
	"context"

	"go.opentelemetry.io/otel/metric/instrument"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	"go.opentelemetry.io/otel/metric/unit"
)

type placementMetrics struct {
	runtimesTotal      syncint64.Counter
	actorRuntimesTotal syncint64.Counter
}

func (m *MetricClient) newPlacementMetrics() *placementMetrics {
	pm := new(placementMetrics)
	pm.runtimesTotal, _ = m.meter.SyncInt64().Counter(
		"placement/runtimes_total",
		instrument.WithDescription("The total number of runtimes reported to placement service."),
		instrument.WithUnit(unit.Dimensionless))
	pm.actorRuntimesTotal, _ = m.meter.SyncInt64().Counter(
		"placement/actor_runtimes_total",
		instrument.WithDescription("The total number of actor runtimes reported to placement service."),
		instrument.WithUnit(unit.Dimensionless))

	return pm
}

// RecordRuntimesCount records the number of connected runtimes.
func (p *placementMetrics) RecordRuntimesCount(count int64) {
	if p == nil {
		return
	}
	p.runtimesTotal.Add(context.Background(), count)
}

// RecordActorRuntimesCount records the number of valid actor runtimes.
func (p *placementMetrics) RecordActorRuntimesCount(count int64) {
	if p == nil {
		return
	}
	p.actorRuntimesTotal.Add(context.Background(), count)
}
