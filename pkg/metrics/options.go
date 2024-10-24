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

package metrics

import (
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/kit/logger"
)

const (
	defaultMetricsPort    = "9090"
	defaultMetricsAddress = "0.0.0.0"
	defaultMetricsEnabled = true
)

// Options defines the sets of options for exporting metrics.
type Options struct {
	// Log is the metrics logger.
	Log logger.Logger
	// Enabled indicates whether a metrics server should be started.
	Enabled bool
	// Namespace is the prometheus exporter namespace.
	Namespace string
	// Port to start metrics server on.
	Port string
	// ListenAddress is the address that the metrics server listens on.
	ListenAddress string
	// Healthz is used to signal the health of the metrics server.
	Healthz healthz.Healthz
	// PushToCollector options for optional push of metrics to external OpenTelemetry collector.
	PushToCollector PushToCollectorOptions
}
type PushToCollectorOptions struct {
	// Enabled indicates whether a metrics should be pushed to otel collector.
	Enabled bool
	// Endpoint hostname and port of the otel collector receiver, example: otelcol:55678
	Endpoint string
	// Headers represents custom http headers that should be passed.
	Headers map[string]string
	// Tls configuration
	TLS TLSOptions
}

type TLSOptions struct {
	// CaFile path to a file with CA cert
	CaFile string
	// CertFile path to a file with public part of the key pair
	CertFile string
	// KeyFile path to a file with private part of the key pair
	KeyFile string
}

type FlagOptions struct {
	enabled       bool
	port          string
	listenAddress string
	PushToCollectorOptions
}

func DefaultFlagOptions() *FlagOptions {
	return &FlagOptions{
		port:    defaultMetricsPort,
		enabled: defaultMetricsEnabled,
		PushToCollectorOptions: PushToCollectorOptions{
			Enabled: false,
		},
	}
}

// MetricsListenAddress gets metrics listen address.
func (o *Options) MetricsListenAddress() string {
	return o.ListenAddress
}

// AttachCmdFlag attaches single metrics option to command flags.
func (f *FlagOptions) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string),
) {
	stringVar(
		&f.port,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
	stringVar(
		&f.listenAddress,
		"metrics-listen-address",
		defaultMetricsAddress,
		"The address for the metrics server")
	boolVar(
		&f.enabled,
		"enable-metrics",
		defaultMetricsEnabled,
		"Enable prometheus metric")
}

func (f *FlagOptions) AttachPusherCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string),
	stringToStringVar func(p *map[string]string, name string, value map[string]string, usage string),
) {
	boolVar(
		&f.PushToCollectorOptions.Enabled,
		"metrics-push-enable",
		false,
		"Enable periodic push of metrics to OTEL collector")
	stringVar(
		&f.PushToCollectorOptions.Endpoint,
		"metrics-push-endpoint",
		"",
		"Hostname and port of the OTEL collector receiver, example: otelcol:55678")
	stringToStringVar(&f.PushToCollectorOptions.Headers,
		"metrics-push-headers",
		map[string]string{},
		"Custom http headers that should be passed when pushing metrics to OTEL collector.",
	)
	stringVar(
		&f.PushToCollectorOptions.TLS.CaFile,
		"metrics-push-tls-ca",
		"",
		"Path to a file with CA cert")
	stringVar(
		&f.PushToCollectorOptions.TLS.CertFile,
		"metrics-push-tls-cert",
		"",
		"Path to a file with public part of the key pair")
	stringVar(
		&f.PushToCollectorOptions.TLS.KeyFile,
		"metrics-push-tls-key",
		"",
		"Path to a file with private part of the key pair")
}

func (f *FlagOptions) ToOptions(healthz healthz.Healthz) Options {
	return Options{
		Enabled:         f.enabled,
		Port:            f.port,
		Healthz:         healthz,
		PushToCollector: f.PushToCollectorOptions,
	}
}

func (f *FlagOptions) Enabled() bool {
	return f.enabled
}

func (f *FlagOptions) Port() string {
	return f.port
}
