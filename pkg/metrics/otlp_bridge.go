/*
Copyright 2024 The Dapr Authors
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

package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/bridge/opencensus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	daprotel "github.com/dapr/dapr/pkg/otel"
	"github.com/dapr/kit/logger"
)

const (
	defaultOTLPExportInterval = 30 * time.Second
	defaultOTLPTimeout        = 15 * time.Second
)

// otlpBridge manages the OpenCensus-to-OpenTelemetry metrics bridge.
// It reads metrics from the global OpenCensus producer and pushes them
// to an OTLP endpoint using the OpenTelemetry SDK's PeriodicReader.
type otlpBridge struct {
	logger        logger.Logger
	meterProvider *sdkmetric.MeterProvider
}

// newOTLPBridge creates a new OTLP metrics bridge from the given options.
// It sets up the OpenCensus producer bridge, OTLP exporter, and PeriodicReader.
func newOTLPBridge(ctx context.Context, opts OTLPOptions, appID string) (*otlpBridge, error) {
	log := logger.NewLogger("dapr.metrics.otlp")

	exporter, err := daprotel.NewMetricExporter(ctx, daprotel.ConnConfig{
		Protocol:       opts.Protocol,
		EndpointAddress: opts.EndpointAddress,
		IsSecure:       opts.IsSecure,
		Headers:        opts.Headers,
		Timeout:        opts.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP metrics exporter: %w", err)
	}

	res, resErr := daprotel.NewResource(ctx, appID)
	if resErr != nil {
		log.Warnf("failed to create OTLP metrics resource from env, using default: %v", resErr)
	}

	exportInterval := defaultOTLPExportInterval
	if opts.ExportInterval > 0 {
		exportInterval = opts.ExportInterval
	}

	// Create the OpenCensus bridge producer. It reads from the global
	// OpenCensus registry and makes OC metrics available to the OTel SDK.
	ocProducer := opencensus.NewMetricProducer()

	// Create the PeriodicReader that collects and exports metrics on a schedule.
	reader := sdkmetric.NewPeriodicReader(
		exporter,
		sdkmetric.WithInterval(exportInterval),
		sdkmetric.WithTimeout(defaultOTLPTimeout),
		sdkmetric.WithProducer(ocProducer),
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)

	log.Infof(
		"OTLP metrics bridge initialized: endpoint=%s protocol=%s interval=%s",
		opts.EndpointAddress, opts.Protocol, exportInterval,
	)

	return &otlpBridge{
		logger:        log,
		meterProvider: provider,
	}, nil
}

// Shutdown gracefully shuts down the OTLP metrics bridge, flushing any pending metrics.
// This follows the OTel recommended pattern: ForceFlush → Shutdown.
func (b *otlpBridge) Shutdown(ctx context.Context) error {
	b.logger.Info("Shutting down OTLP metrics bridge")

	if err := b.meterProvider.ForceFlush(ctx); err != nil {
		b.logger.Warnf("Error flushing OTLP metrics: %v", err)
	}

	return b.meterProvider.Shutdown(ctx)
}

// ForceFlush forces a flush of any pending metrics.
func (b *otlpBridge) ForceFlush(ctx context.Context) error {
	return b.meterProvider.ForceFlush(ctx)
}
