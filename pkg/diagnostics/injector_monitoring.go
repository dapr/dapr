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

	isemconv "github.com/dapr/dapr/pkg/diagnostics/semconv"
)

type injectorMetrics struct {
	sidecarInjectionRequestsTotal syncint64.Counter
	succeededSidecarInjectedTotal syncint64.Counter
	failedSidecarInjectedTotal    syncint64.Counter
}

func (m *MetricClient) newInjectorMetrics() *injectorMetrics {
	im := new(injectorMetrics)
	im.sidecarInjectionRequestsTotal, _ = m.meter.SyncInt64().Counter(
		"injector/sidecar_injection/requests_total",
		instrument.WithDescription("The total number of sidecar injection requests."),
		instrument.WithUnit(unit.Dimensionless))
	im.succeededSidecarInjectedTotal, _ = m.meter.SyncInt64().Counter(
		"injector/sidecar_injection/succeeded_total",
		instrument.WithDescription("The total number of successful sidecar injections."),
		instrument.WithUnit(unit.Dimensionless))
	im.failedSidecarInjectedTotal, _ = m.meter.SyncInt64().Counter(
		"injector/sidecar_injection/failed_total",
		instrument.WithDescription("The total number of failed sidecar injections."),
		instrument.WithUnit(unit.Dimensionless))

	return im
}

// RecordSidecarInjectionRequestsCount records the total number of sidecar injection requests.
func (i *injectorMetrics) RecordSidecarInjectionRequestsCount() {
	if i == nil {
		return
	}
	i.sidecarInjectionRequestsTotal.Add(context.Background(), 1)
}

// RecordSuccessfulSidecarInjectionCount records the number of successful sidecar injections.
func (i *injectorMetrics) RecordSuccessfulSidecarInjectionCount(appID string) {
	if i == nil {
		return
	}
	i.succeededSidecarInjectedTotal.Add(context.Background(), 1,
		semconv.ServiceNameKey.String(appID))
}

// RecordFailedSidecarInjectionCount records the number of failed sidecar injections.
func (i *injectorMetrics) RecordFailedSidecarInjectionCount(appID, reason string) {
	if i == nil {
		return
	}
	i.failedSidecarInjectedTotal.Add(context.Background(), 1,
		semconv.ServiceNameKey.String(appID),
		isemconv.FailReasonKey.String(reason))
}
