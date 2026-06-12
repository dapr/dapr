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

package otel

import (
	"context"
	"time"

	otlploggrpc "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlploghttp "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otlpmetricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlpmetrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"

	"github.com/dapr/dapr/pkg/buildinfo"
)

// ConnConfig holds shared OTLP connection parameters used by all signal exporters.
// Each signal bridge converts its signal-specific options into a ConnConfig
// and passes it to the shared exporter constructors.
type ConnConfig struct {
	// Protocol is the OTLP transport protocol ("grpc" or "http").
	Protocol string
	// EndpointAddress is the OTLP receiver endpoint (host:port).
	EndpointAddress string
	// IsSecure indicates whether to use TLS.
	IsSecure bool
	// Headers to add to export requests.
	Headers map[string]string
	// Timeout for export requests. Zero means use SDK default.
	Timeout time.Duration
}

// NewResource creates an OTel resource with service attributes.
// It uses WithFromEnv to respect OTEL_RESOURCE_ATTRIBUTES and OTEL_SERVICE_NAME,
// matching the pattern used by Dapr's tracing. Falls back to a minimal resource
// with just service.name and service.version if env-based creation fails.
func NewResource(ctx context.Context, appID string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(appID),
			semconv.ServiceVersionKey.String(buildinfo.Version()),
		),
		resource.WithSchemaURL(semconv.SchemaURL),
	)
	if err != nil {
		// Fall back to a minimal resource without env-based attributes.
		return resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(appID),
			semconv.ServiceVersionKey.String(buildinfo.Version()),
		), err
	}
	return res, nil
}

// NewMetricExporter creates an OTLP metric exporter based on the connection config.
// Protocol "http" uses otlpmetrichttp; all other values default to gRPC.
func NewMetricExporter(ctx context.Context, cfg ConnConfig) (sdkmetric.Exporter, error) {
	if cfg.Protocol == "http" {
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.EndpointAddress),
		}
		if !cfg.IsSecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlpmetrichttp.WithTimeout(cfg.Timeout))
		}
		return otlpmetrichttp.New(ctx, opts...)
	}

	// Default to gRPC
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.EndpointAddress),
	}
	if !cfg.IsSecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, otlpmetricgrpc.WithTimeout(cfg.Timeout))
	}
	return otlpmetricgrpc.New(ctx, opts...)
}

// NewLogExporter creates an OTLP log exporter based on the connection config.
// Protocol "http" uses otlploghttp; all other values default to gRPC.
func NewLogExporter(ctx context.Context, cfg ConnConfig) (sdklog.Exporter, error) {
	if cfg.Protocol == "http" {
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(cfg.EndpointAddress),
		}
		if !cfg.IsSecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlploghttp.WithTimeout(cfg.Timeout))
		}
		return otlploghttp.New(ctx, opts...)
	}

	// Default to gRPC
	opts := []otlploggrpc.Option{
		otlploggrpc.WithEndpoint(cfg.EndpointAddress),
	}
	if !cfg.IsSecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(cfg.Headers))
	}
	if cfg.Timeout > 0 {
		opts = append(opts, otlploggrpc.WithTimeout(cfg.Timeout))
	}
	return otlploggrpc.New(ctx, opts...)
}
