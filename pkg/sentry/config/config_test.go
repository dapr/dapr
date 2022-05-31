package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	dapr_config "github.com/dapr/dapr/pkg/config"
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
		daprConfig := dapr_config.Configuration{
			Spec: dapr_config.ConfigurationSpec{
				MTLSSpec: dapr_config.MTLSSpec{
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
