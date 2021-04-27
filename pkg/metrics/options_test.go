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
		assert.Equal(t, defaultMetricsPort, o.Port)
		assert.Equal(t, defaultMetricsEnabled, o.MetricsEnabled)
	})

	t.Run("attaching metrics related cmd flags", func(t *testing.T) {
		o := defaultMetricOptions()

		metricsPortAsserted := false
		testStringVarFn := func(p *string, name string, value string, usage string) {
			if name == "metrics-port" && value == defaultMetricsPort {
				metricsPortAsserted = true
			}
		}

		metricsEnabledAsserted := false
		testBoolVarFn := func(p *bool, name string, value bool, usage string) {
			if name == "enable-metrics" && value == defaultMetricsEnabled {
				metricsEnabledAsserted = true
			}
		}

		o.AttachCmdFlags(testStringVarFn, testBoolVarFn)

		// assert
		assert.True(t, metricsPortAsserted)
		assert.True(t, metricsEnabledAsserted)
	})

	t.Run("parse valid port", func(t *testing.T) {
		o := Options{
			Port:           "1010",
			MetricsEnabled: false,
		}

		assert.Equal(t, uint64(1010), o.MetricsPort())
	})

	t.Run("return default port if port is invalid", func(t *testing.T) {
		o := Options{
			Port:           "invalid",
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
