// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"fortio.org/fortio/log"

	"go.opentelemetry.io/otel/api/global"
	"go.opentelemetry.io/otel/api/metric"
	"go.opentelemetry.io/otel/exporters/metric/prometheus"
	"go.opentelemetry.io/otel/label"
)

const (
	metricsPort = 9988
)

// TelemetryClient is the client to record metrics of the test.
type TelemetryClient struct {
	meter      metric.Meter
	reqCounter metric.Int64Counter
	reqLatency metric.Int64ValueRecorder

	hostname string
}

// NewTelemetryClient creates new telemetry client.
func NewTelemetryClient() *TelemetryClient {
	meter := global.Meter("dapr.io/actorload")

	reqCounter := metric.Must(meter).NewInt64Counter("actorload.runner.reqcount")
	reqLatency := metric.Must(meter).NewInt64ValueRecorder("actorload.runner.reqlatency")
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
	exporter, err := prometheus.InstallNewPipeline(prometheus.Config{})
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
	t.meter.RecordBatch(
		context.Background(),
		[]label.KeyValue{
			label.String("hostname", t.hostname),
			label.Int("code", code),
			label.Bool("success", code == 200),
			label.String("actor", fmt.Sprintf("%s.%s", actorType, actorID))},
		t.reqCounter.Measurement(1))

	t.reqLatency.Record(
		context.Background(),
		elapsed.Milliseconds(),
		label.String("hostname", t.hostname),
		label.Int("code", code),
		label.Bool("success", code == 200),
		label.String("actor", fmt.Sprintf("%s.%s", actorType, actorID)))

}
