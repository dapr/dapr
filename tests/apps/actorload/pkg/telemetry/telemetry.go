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

package telemetry

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/metric/instrument/syncint64"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	"go.opentelemetry.io/otel/sdk/metric/export/aggregation"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	selector "go.opentelemetry.io/otel/sdk/metric/selector/simple"
)

const (
	metricsPort = 9988
)

// TelemetryClient is the client to record metrics of the test.
type TelemetryClient struct {
	meter      metric.Meter
	reqCounter syncint64.Counter
	reqLatency syncint64.Histogram

	hostname string
}

// NewTelemetryClient creates new telemetry client.
func NewTelemetryClient() *TelemetryClient {
	meter := global.MeterProvider().Meter("dapr.io/actorload")

	reqCounter, _ := meter.SyncInt64().Counter("actorload.runner.reqcount")
	reqLatency, _ := meter.SyncInt64().Histogram("actorload.runner.reqlatency")

	hostname, _ := os.Hostname()

	return &TelemetryClient{
		meter:      meter,
		reqCounter: reqCounter,
		reqLatency: reqLatency,
		hostname:   hostname,
	}
}

// Init initializes metrics pipeline and starts metrics http endpoint.
func (t *TelemetryClient) Init() {
	ctl := controller.New(processor.NewFactory(
		selector.NewWithHistogramDistribution(),
		aggregation.CumulativeTemporalitySelector(),
		processor.WithMemory(true),
	))

	exporter, err := prometheus.New(prometheus.Config{}, ctl)
	if err != nil {
		log.Fatalf("failed to initialize prometheus exporter %v", err)
	}

	http.HandleFunc("/metrics", exporter.ServeHTTP)
	go func() {
		_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", metricsPort), nil)
	}()

	fmt.Printf("Prometheus server running on :%d", metricsPort)
}

// RecordLoadRequestCount records request count and latency.
func (t *TelemetryClient) RecordLoadRequestCount(actorType, actorID string, elapsed time.Duration, code int) {
	t.reqCounter.Add(
		context.Background(), 1,
		attribute.String("hostname", t.hostname),
		attribute.Int("code", code),
		attribute.Bool("success", code == 200),
		attribute.String("actor", fmt.Sprintf("%s.%s", actorType, actorID)),
	)

	t.reqLatency.Record(
		context.Background(),
		elapsed.Milliseconds(),
		attribute.String("hostname", t.hostname),
		attribute.Int("code", code),
		attribute.Bool("success", code == 200),
		attribute.String("actor", fmt.Sprintf("%s.%s", actorType, actorID)),
	)
}
