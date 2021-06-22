// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
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

// Options defines the sets of options for Dapr logging.
type Options struct {
	// OutputLevel is the level of logging
	MetricsEnabled bool

	Port string
}

func defaultMetricOptions() *Options {
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
	boolVar func(p *bool, name string, value bool, usage string)) {
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
	stringVar func(p *string, name string, value string, usage string)) {
	stringVar(
		&o.Port,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
}
