// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics

import (
	"strconv"
)

const (
	defaultMetricsPort    = "9090"
	defaultMetricsEnabled = true
)

// Options defines the sets of options for Dapr logging
type Options struct {
	// OutputLevel is the level of logging
	MetricsEnabled bool

	metricsPort string
}

func defaultMetricOptions() *Options {
	return &Options{
		metricsPort:    defaultMetricsPort,
		MetricsEnabled: defaultMetricsEnabled,
	}
}

// MetricsPort gets metrics port.
func (o *Options) MetricsPort() uint64 {
	port, err := strconv.ParseUint(o.metricsPort, 10, 64)
	if err != nil {
		// Use default metrics port as a fallback
		port, _ = strconv.ParseUint(defaultMetricsPort, 10, 64)
	}

	return port
}

// AttachCmdFlags attaches metrics options to command flags
func (o *Options) AttachCmdFlags(
	stringVar func(p *string, name string, value string, usage string),
	boolVar func(p *bool, name string, value bool, usage string)) {
	stringVar(
		&o.metricsPort,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
	boolVar(
		&o.MetricsEnabled,
		"enable-metrics",
		defaultMetricsEnabled,
		"Enable prometheus metric")
}
