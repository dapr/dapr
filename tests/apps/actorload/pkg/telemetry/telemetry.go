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
