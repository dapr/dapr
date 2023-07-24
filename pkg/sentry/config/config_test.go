package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	daprDaprConfig "github.com/dapr/dapr/pkg/config"
)

func TestConfig(t *testing.T) {
	t.Run("valid FromConfig, empty config name", func(t *testing.T) {
		defaultConfig := getDefaultConfig()
		c, _ := FromConfigName("")

		assert.Equal(t, defaultConfig, c)
	})

	t.Run("valid default config, self hosted", func(t *testing.T) {
		defaultConfig := getDefaultConfig()
		c, _ := getSelfhostedConfig("")

		assert.Equal(t, defaultConfig, c)
	})

	t.Run("parse configuration", func(t *testing.T) {
		daprConfig := daprDaprConfig.Configuration{
			Spec: daprDaprConfig.ConfigurationSpec{
				MTLSSpec: &daprDaprConfig.MTLSSpec{
					Enabled:          true,
					WorkloadCertTTL:  "5s",
					AllowedClockSkew: "1h",
				},
			},
		}

		defaultConfig := getDefaultConfig()
		conf, err := parseConfiguration(defaultConfig, &daprConfig)
		assert.Nil(t, err)
		assert.Equal(t, "5s", conf.WorkloadCertTTL.String())
		assert.Equal(t, "1h0m0s", conf.AllowedClockSkew.String())
	})
}
