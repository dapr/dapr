// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics

import (
	"github.com/pkg/errors"
	"strconv"
)

const (
	defaultMetricsPort                      = "9090"
	defaultMetricsEnabled                   = true
	defaultMetricsPushGatewayEnabled        = false
	defaultMetricsPushGatewayHost           = "127.0.0.1"
	defaultMetricsPushGatewayPort           = "9091"
	defaultMetricsPushGatewayUpdateInterval = 15
)

// Options defines the sets of options for Dapr logging
type Options struct {
	// OutputLevel is the level of logging
	MetricsEnabled bool

	metricsPort string

	// pushGatewayEnabled defines if prometheus pushgateway upload is enabled, it only works when MetricsEnabled is true
	pushGatewayEnabled bool

	// pushGatewayHost is the ip of prometheus pushgateway
	pushGatewayHost string

	// pushGatewayPort is the port of prometheus pushgateway
	pushGatewayPort string

	// pushGatewayUpdateInterval is the update time interval
	pushGatewayUpdateInterval int
}

func defaultMetricOptions() *Options {
	return &Options{
		metricsPort:               defaultMetricsPort,
		MetricsEnabled:            defaultMetricsEnabled,
		pushGatewayEnabled:        defaultMetricsPushGatewayEnabled,
		pushGatewayHost:           defaultMetricsPushGatewayHost,
		pushGatewayPort:           defaultMetricsPushGatewayPort,
		pushGatewayUpdateInterval: defaultMetricsPushGatewayUpdateInterval,
	}
}

// CheckParam Check if the option is valid
func (o *Options) CheckParam() error {
	if o.pushGatewayUpdateInterval <= 0 {
		return errors.Errorf("pushgatewat update invalid, it must be positive integer")
	}

	if _, err := strconv.Atoi(o.pushGatewayPort); err != nil {
		return err
	}

	return nil
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
	boolVar func(p *bool, name string, value bool, usage string),
	intVar func(p *int, name string, value int, usage string)) {
	stringVar(
		&o.metricsPort,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
	stringVar(
		&o.pushGatewayHost,
		"metrics-pushgateway-host",
		defaultMetricsPushGatewayHost,
		"The host for the pushgateway server")
	stringVar(
		&o.pushGatewayPort,
		"metrics-pushgateway-port",
		defaultMetricsPushGatewayPort,
		"The port for the pushgateway server")
	intVar(
		&o.pushGatewayUpdateInterval,
		"metrics-pushgateway-interval",
		defaultMetricsPushGatewayUpdateInterval,
		"The update interval time (seconds) to pushgateway server")
	boolVar(
		&o.MetricsEnabled,
		"enable-metrics",
		defaultMetricsEnabled,
		"Enable prometheus metric")
	boolVar(
		&o.pushGatewayEnabled,
		"enable-metrics-pushgateway",
		defaultMetricsPushGatewayEnabled,
		"Enable prometheus metric pushgateway")
}

// AttachCmdFlag attaches single metrics option to command flags
func (o *Options) AttachCmdFlag(
	stringVar func(p *string, name string, value string, usage string)) {
	stringVar(
		&o.metricsPort,
		"metrics-port",
		defaultMetricsPort,
		"The port for the metrics server")
}
