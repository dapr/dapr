/*
Copyright 2023 The Dapr Authors
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

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	daprDaprConfig "github.com/dapr/dapr/pkg/config"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
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
		require.NoError(t, err)
		assert.Equal(t, "5s", conf.WorkloadCertTTL.String())
		assert.Equal(t, "1h0m0s", conf.AllowedClockSkew.String())
	})

	t.Run("set validators", func(t *testing.T) {
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

		t.Run("kubernetes mode", func(t *testing.T) {
			// Setting this env var makes Sentry think we're running on Kubernetes
			t.Setenv(kubernetesServiceHostEnvVar, "TEST")

			t.Run("no additional validators", func(t *testing.T) {
				daprConfig.Spec.MTLSSpec.TokenValidators = nil

				conf, err := parseConfiguration(defaultConfig, &daprConfig)
				require.NoError(t, err)

				require.Len(t, conf.Validators, 1)
				require.NotNil(t, conf.Validators[sentryv1pb.SignCertificateRequest_KUBERNETES])
				require.Equal(t, sentryv1pb.SignCertificateRequest_KUBERNETES, conf.DefaultValidator)
			})

			t.Run("additional validators", func(t *testing.T) {
				daprConfig.Spec.MTLSSpec.TokenValidators = []daprDaprConfig.ValidatorSpec{
					{Name: sentryv1pb.SignCertificateRequest_JWKS.String(), Options: map[any]any{"foo": "bar"}},
				}

				conf, err := parseConfiguration(defaultConfig, &daprConfig)
				require.NoError(t, err)

				require.Len(t, conf.Validators, 2)
				require.NotNil(t, conf.Validators[sentryv1pb.SignCertificateRequest_KUBERNETES])
				require.NotNil(t, conf.Validators[sentryv1pb.SignCertificateRequest_JWKS])
				require.Equal(t, map[string]string{"foo": "bar"}, conf.Validators[sentryv1pb.SignCertificateRequest_JWKS])
				require.Equal(t, sentryv1pb.SignCertificateRequest_KUBERNETES, conf.DefaultValidator)
			})
		})

		t.Run("self-hosted mode", func(t *testing.T) {
			// Deleting this env var to empty makes Sentry think we're running on self-hosted mode
			t.Setenv(kubernetesServiceHostEnvVar, "")

			t.Run("no additional validators", func(t *testing.T) {
				daprConfig.Spec.MTLSSpec.TokenValidators = nil

				conf, err := parseConfiguration(defaultConfig, &daprConfig)
				require.NoError(t, err)

				require.Len(t, conf.Validators, 1)
				require.NotNil(t, conf.Validators[sentryv1pb.SignCertificateRequest_INSECURE])
				require.Equal(t, sentryv1pb.SignCertificateRequest_INSECURE, conf.DefaultValidator)
			})

			t.Run("additional validators", func(t *testing.T) {
				daprConfig.Spec.MTLSSpec.TokenValidators = []daprDaprConfig.ValidatorSpec{
					{Name: sentryv1pb.SignCertificateRequest_JWKS.String(), Options: map[any]any{"foo": "bar"}},
				}

				conf, err := parseConfiguration(defaultConfig, &daprConfig)
				require.NoError(t, err)

				require.Len(t, conf.Validators, 1)
				require.NotNil(t, conf.Validators[sentryv1pb.SignCertificateRequest_JWKS])
				require.Equal(t, map[string]string{"foo": "bar"}, conf.Validators[sentryv1pb.SignCertificateRequest_JWKS])
				require.Equal(t, 0, int(conf.DefaultValidator))
			})
		})
	})
}
