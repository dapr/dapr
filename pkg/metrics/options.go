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
}

type FlagOptions struct {
	enabled       bool
	port          string
	listenAddress string
}

func DefaultFlagOptions() *FlagOptions {
	return &FlagOptions{
		port:    defaultMetricsPort,
		enabled: defaultMetricsEnabled,
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

func (f *FlagOptions) ToOptions(healthz healthz.Healthz) Options {
	return Options{
		Enabled: f.enabled,
		Port:    f.port,
		Healthz: healthz,
	}
}

func (f *FlagOptions) Enabled() bool {
	return f.enabled
}

func (f *FlagOptions) Port() string {
	return f.port
}

func (f *FlagOptions) ListenAddress() string {
	return f.listenAddress
}
