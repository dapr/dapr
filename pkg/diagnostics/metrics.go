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
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/sdk/metric/aggregator/histogram"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.9.0"
)

var (
	// DefaultReportingPeriod is the default view reporting period.
	DefaultReportingPeriod = 60 * time.Second

	// DefaultMonitoring holds service monitoring metrics definitions.
	DefaultMonitoring *serviceMetrics
	// DefaultGRPCMonitoring holds default gRPC monitoring handlers and middlewares.
	DefaultGRPCMonitoring *grpcMetrics
	// DefaultHTTPMonitoring holds default HTTP monitoring handlers and middlewares.
	DefaultHTTPMonitoring *httpMetrics
	// DefaultComponentMonitoring holds component specific metrics.
	DefaultComponentMonitoring *componentMetrics
	// DefaultResiliencyMonitoring holds resiliency specific metrics.
	DefaultResiliencyMonitoring *resiliencyMetrics

	// DefaultPlacementMonitoring holds placement specific metrics.
	DefaultPlacementMonitoring *placementMetrics
	// DefaultSentryMonitoring holds sentry specific metrics.
	DefaultSentryMonitoring *sentryMetrics
	// DefaultOperatorMonitoring holds operator specific metrics.
	DefaultOperatorMonitoring *operatorMetrics
	// DefaultInjectorMonitoring holds injector specific metrics.
	DefaultInjectorMonitoring *injectorMetrics
)

// ServiceType service type, such as dapr system service or dapr sidecar.
type ServiceType string

const (
	Daprd     ServiceType = "daprd"
	Placement ServiceType = "placement"
	Sentry    ServiceType = "sentry"
	Injector  ServiceType = "injector"
	Operator  ServiceType = "operator"
)

// MetricClient is a metric client.
type MetricClient struct {
	AppID     string
	Namespace string
	// Address collector receiver address.
	Address string

	meter    metric.Meter
	pusher   *controller.Controller
	exporter *otlpmetric.Exporter
}

// InitMetrics initializes metrics.
func InitMetrics(serviceType ServiceType, address, appID, namespace string) (*MetricClient, error) {
	var err error
	if address == "" {
		address = defaultMetricExporterAddr
	}
	client := &MetricClient{
		AppID:     appID,
		Namespace: namespace,
		Address:   address,
	}
	if err = client.init(); err != nil {
		return nil, err
	}

	switch serviceType {
	case Daprd:
		DefaultMonitoring = client.newServiceMetrics()
		DefaultGRPCMonitoring = client.newGRPCMetrics()
		DefaultHTTPMonitoring = client.newHTTPMetrics()
		DefaultComponentMonitoring = client.newComponentMetrics()
		DefaultResiliencyMonitoring = client.newResiliencyMetrics()
	case Placement:
		DefaultPlacementMonitoring = client.newPlacementMetrics()
	case Operator:
		DefaultOperatorMonitoring = client.newOperatorMetrics()
	case Injector:
		DefaultInjectorMonitoring = client.newInjectorMetrics()
	case Sentry:
		DefaultSentryMonitoring = client.newSentryMetrics()
	default:
		return nil, errors.Errorf("unknown service type: %s", serviceType)
	}

	return client, nil
}

func (m *MetricClient) init() error {
	var err error
	ctx := context.Background()
	client := otlpmetricgrpc.NewClient(
		otlpmetricgrpc.WithInsecure(),
		otlpmetricgrpc.WithEndpoint(m.Address))
	m.exporter, err = otlpmetric.New(ctx, client)
	if err != nil {
		return errors.Errorf("Failed to create the collector exporter: %v", err)
	}
	res, _ := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(m.AppID),
			semconv.ServiceNamespaceKey.String(m.Namespace),
		),
	)
	// ::TODO https://github.com/open-telemetry/opentelemetry-go/issues/2678
	// fake boundary
	bounds := []float64{5, 10, 50, 100, 150, 200, 250, 300, 350, 400, 500, 600, 700, 800, 900, 1000}
	m.pusher = controller.New(
		processor.NewFactory(
			simple.NewWithHistogramDistribution(
				histogram.WithExplicitBoundaries(
					bounds)),
			m.exporter,
		),
		controller.WithExporter(m.exporter),
		controller.WithCollectPeriod(DefaultReportingPeriod),
		controller.WithCollectTimeout(30*time.Second),
		controller.WithPushTimeout(30*time.Second),
		controller.WithResource(res))
	global.SetMeterProvider(m.pusher)
	// only global one meter, not multiple meters.
	m.meter = global.Meter("dapr",
		metric.WithInstrumentationVersion("v0.31.0"),
		metric.WithSchemaURL("https://dapr.io"))

	if err := m.pusher.Start(ctx); err != nil {
		return errors.Errorf("could not start metric controller: %v", err)
	}
	return nil
}

// Close close metric client.
func (m *MetricClient) Close() error {
	if m == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.pusher.Stop(ctx); err != nil {
		otel.Handle(err)
	}
	if err := m.exporter.Shutdown(ctx); err != nil {
		otel.Handle(err)
	}
	return nil
}
