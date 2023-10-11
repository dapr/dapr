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
	"strconv"
)

const (
	defaultMetricsPort    = "9090"
	defaultMetricsEnabled = true
)

// Options defines the sets of options for exporting metrics.
type Options struct {
	// MetricsEnabled indicates whether a metrics server should be started.
	MetricsEnabled bool
	// Port to start metrics server on.
	Port string
}

func DefaultMetricOptions() *Options {
	return &Options{
		Port:           defaultMetricsPort,
		MetricsEnabled: defaultMetricsEnabled,
	}
}

// MetricsPort gets metrics port.
func (o *Options) MetricsPort() uint64 {
	port, err := strconv.ParseUint(o.Port, 10, 64)
	if err != nil {
		// Use default metrics port as a fallback
		port, _ = strconv.ParseUint(defaultMetricsPort, 10, 64)
	}

	return port
}

// AttachCmdFlags attaches metrics options to command flags.
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string),
) {
	stringVar(
		&o.Port,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
	boolVar(
		&o.MetricsEnabled,
		"enable-metrics",
		defaultMetricsEnabled,
		"Enable prometheus metric")
}

// AttachCmdFlag attaches single metrics option to command flags.
func (o *Options) AttachCmdFlag(
	stringVar func(p *string, name string, value string, usage string),
) {
	stringVar(
		&o.Port,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
}
