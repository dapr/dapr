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
	"fmt"
	"time"

	hostinstrumentation "go.opentelemetry.io/contrib/instrumentation/host"
	runtimeinstrumentation "go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	ocbridge "go.opentelemetry.io/otel/bridge/opencensus"
	"go.opentelemetry.io/otel/exporters/prometheus"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/kit/logger"
)

// appIDKey is a tag key for App ID.
var appIDKey = "app_id"

var (
	// DefaultReportingPeriod is the default view reporting period.
	DefaultReportingPeriod = 1 * time.Minute

	DefaultMonitoring = newServiceMetrics()
	// DefaultGRPCMonitoring holds default gRPC monitoring handlers and middlewares.
	DefaultGRPCMonitoring = newGRPCMetrics()
	// DefaultHTTPMonitoring holds default HTTP monitoring handlers and middlewares.
	DefaultHTTPMonitoring = newHTTPMetrics()
	// DefaultComponentMonitoring holds component specific metrics.
	DefaultComponentMonitoring = newComponentMetrics()
	// DefaultResiliencyMonitoring holds resiliency specific metrics.
	DefaultResiliencyMonitoring = newResiliencyMetrics()
	// DefaultWorkflowMonitoring holds workflow specific metrics.
	DefaultWorkflowMonitoring = newWorkflowMetrics()
)

// InitMetrics initializes metrics.
func InitMetrics(ctx context.Context, appID, namespace string, rules []config.MetricsRule, legacyMetricsHTTPMetrics bool, openCensusProducer bool) (func(log logger.Logger), error) {
	res, err := resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("daprd"),
			semconv.ServiceVersion(buildinfo.Version()),
		))
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics resource: %s", err)
	}

	var opts []prometheus.Option
	if openCensusProducer {
		// enable open census bridge
		opts = append(opts, prometheus.WithProducer(ocbridge.NewMetricProducer()))
	}

	metricExporter, err := prometheus.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric exporter: %s", err)
	}

	// TODO: Allow configuration of different exporters.

	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metricExporter),
	)

	// register this meter provider as the global default.
	otel.SetMeterProvider(meterProvider)

	if err := DefaultMonitoring.Init(appID); err != nil {
		return nil, err
	}

	if err := DefaultGRPCMonitoring.Init(appID); err != nil {
		return nil, err
	}

	if err := DefaultHTTPMonitoring.Init(appID, legacyMetricsHTTPMetrics); err != nil {
		return nil, err
	}

	if err := DefaultComponentMonitoring.Init(appID, namespace); err != nil {
		return nil, err
	}

	if err := DefaultResiliencyMonitoring.Init(appID); err != nil {
		return nil, err
	}

	if err := DefaultWorkflowMonitoring.Init(appID, namespace); err != nil {
		return nil, err
	}

	// TODO: implement this for otel.
	err = utils.CreateRulesMap(rules)
	if err != nil {
		return nil, err
	}

	shutdown := func(log logger.Logger) {
		if err := metricExporter.Shutdown(context.Background()); err != nil {
			log.Errorf("failed to shutdown metric exporter: %s", err)
		}
	}

	if err = runtimeinstrumentation.Start(); err != nil {
		return shutdown, fmt.Errorf("failed to start runtime instrumentation: %s", err)
	}

	if err = hostinstrumentation.Start(); err != nil {
		return shutdown, fmt.Errorf("failed to start host instrumentation: %s", err)
	}

	return shutdown, nil
}
