// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package metrics

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		o := defaultMetricOptions()
		assert.Equal(t, defaultMetricsPort, o.metricsPort)
		assert.Equal(t, defaultMetricsEnabled, o.MetricsEnabled)
		assert.Equal(t, defaultMetricsPushGatewayHost, o.pushGatewayHost)
		assert.Equal(t, defaultMetricsPushGatewayPort, o.pushGatewayPort)
		assert.Equal(t, defaultMetricsPushGatewayEnabled, o.pushGatewayEnabled)
		assert.Equal(t, defaultMetricsPushGatewayUpdateInterval, o.pushGatewayUpdateInterval)
	})

	t.Run("attaching metrics related cmd flags", func(t *testing.T) {
		o := defaultMetricOptions()

		metricsPortAsserted := false
		metricsPushGatewayHostAsserted := false
		metricsPushGatewayPortAsserted := false
		metricsPushGatewayEnabledAsserted := false
		metricsPushGatewayIntervalAsserted := false
		testStringVarFn := func(p *string, name string, value string, usage string) {
			if name == "metrics-port" && value == defaultMetricsPort {
				metricsPortAsserted = true
			}

			if name == "metrics-pushgateway-host" && value == defaultMetricsPushGatewayHost {
				metricsPushGatewayHostAsserted = true
			}

			if name == "metrics-pushgateway-port" && value == defaultMetricsPushGatewayPort {
				metricsPushGatewayPortAsserted = true
			}

			if name == "metrics-port" && value == defaultMetricsPort {
				metricsPortAsserted = true
			}
		}

		metricsEnabledAsserted := false
		testBoolVarFn := func(p *bool, name string, value bool, usage string) {
			if name == "enable-metrics" && value == defaultMetricsEnabled {
				metricsEnabledAsserted = true
			}

			if name == "enable-metrics-pushgateway" && value == defaultMetricsPushGatewayEnabled {
				metricsPushGatewayEnabledAsserted = true
			}
		}

		testIntVarFn := func(p *int, name string, value int, usage string) {
			if name == "metrics-pushgateway-interval" && value == defaultMetricsPushGatewayUpdateInterval {
				metricsPushGatewayIntervalAsserted = true
			}
		}

		o.AttachCmdFlags(testStringVarFn, testBoolVarFn, testIntVarFn)

		// assert
		assert.True(t, metricsPortAsserted)
		assert.True(t, metricsEnabledAsserted)
		assert.True(t, metricsPushGatewayHostAsserted)
		assert.True(t, metricsPushGatewayPortAsserted)
		assert.True(t, metricsPushGatewayIntervalAsserted)
		assert.True(t, metricsPushGatewayEnabledAsserted)
	})

	t.Run("parse valid port", func(t *testing.T) {
		o := Options{
			metricsPort:    "1010",
			MetricsEnabled: false,
		}

		assert.Equal(t, uint64(1010), o.MetricsPort())
	})

	t.Run("return default port if port is invalid", func(t *testing.T) {
		o := Options{
			metricsPort:    "invalid",
			MetricsEnabled: false,
		}

		defaultPort, _ := strconv.ParseUint(defaultMetricsPort, 10, 64)

		assert.Equal(t, defaultPort, o.MetricsPort())
	})

	t.Run("attaching single metrics related cmd flag", func(t *testing.T) {
		o := defaultMetricOptions()

		metricsPortAsserted := false
		testStringVarFn := func(p *string, name string, value string, usage string) {
			if name == "metrics-port" && value == defaultMetricsPort {
				metricsPortAsserted = true
			}
		}

		o.AttachCmdFlag(testStringVarFn)

		// assert
		assert.True(t, metricsPortAsserted)
	})
}
