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
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

type operatorMetrics struct {
	serviceCreatedTotal syncint64.Counter
	serviceDeletedTotal syncint64.Counter
	serviceUpdatedTotal syncint64.Counter
}

func (m *MetricClient) newOperatorMetrics() *operatorMetrics {
	om := new(operatorMetrics)
	om.serviceCreatedTotal, _ = m.meter.SyncInt64().Counter(
		"operator/service_created_total",
		instrument.WithDescription("The total number of dapr services created."),
		instrument.WithUnit(unit.Dimensionless))
	om.serviceDeletedTotal, _ = m.meter.SyncInt64().Counter(
		"operator/service_deleted_total",
		instrument.WithDescription("The total number of dapr services deleted."),
		instrument.WithUnit(unit.Dimensionless))
	om.serviceUpdatedTotal, _ = m.meter.SyncInt64().Counter(
		"operator/service_updated_total",
		instrument.WithDescription("The total number of dapr services updated."),
		instrument.WithUnit(unit.Dimensionless))

	return om
}

// RecordServiceCreatedCount records the number of dapr service created.
func (o *operatorMetrics) RecordServiceCreatedCount(appID string) {
	if o == nil {
		return
	}
	o.serviceCreatedTotal.Add(context.Background(), 1,
		semconv.ServiceNameKey.String(appID))
}

// RecordServiceDeletedCount records the number of dapr service deleted.
func (o *operatorMetrics) RecordServiceDeletedCount(appID string) {
	if o == nil {
		return
	}
	o.serviceDeletedTotal.Add(context.Background(), 1,
		semconv.ServiceNameKey.String(appID))
}

// RecordServiceUpdatedCount records the number of dapr service updated.
func (o *operatorMetrics) RecordServiceUpdatedCount(appID string) {
	if o == nil {
		return
	}
	o.serviceUpdatedTotal.Add(context.Background(), 1,
		semconv.ServiceNameKey.String(appID))
}
