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

package config

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/maps"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

func TestLoadStandaloneConfiguration(t *testing.T) {
	testCases := []struct {
		name          string
		path          string
		errorExpected bool
	}{
		{
			name:          "Valid config file",
			path:          "./testdata/config.yaml",
			errorExpected: false,
		},
		{
			name:          "Invalid file path",
			path:          "invalid_file.yaml",
			errorExpected: true,
		},
		{
			name:          "Invalid config file",
			path:          "./testdata/invalid_secrets_config.yaml",
			errorExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := LoadStandaloneConfiguration(tc.path)
			if tc.errorExpected {
				require.Error(t, err, "Expected an error")
				assert.Nil(t, config, "Config should not be loaded")
			} else {
				require.NoError(t, err, "Unexpected error")
				assert.NotNil(t, config, "Config not loaded as expected")
			}
		})
	}

	t.Run("parse environment variables", func(t *testing.T) {
		t.Setenv("DAPR_SECRET", "keepitsecret")
		config, err := LoadStandaloneConfiguration("./testdata/env_variables_config.yaml")
		require.NoError(t, err, "Unexpected error")
		assert.NotNil(t, config, "Config not loaded as expected")
		assert.Equal(t, "keepitsecret", config.Spec.Secrets.Scopes[0].AllowedSecrets[0])
	})

	t.Run("check Kind and Name", func(t *testing.T) {
		config, err := LoadStandaloneConfiguration("./testdata/config.yaml")
		require.NoError(t, err, "Unexpected error")
		assert.NotNil(t, config, "Config not loaded as expected")
		assert.Equal(t, "secretappconfig", config.ObjectMeta.Name)
		assert.Equal(t, "Configuration", config.TypeMeta.Kind)
	})

	t.Run("metrics spec", func(t *testing.T) {
		testCases := []struct {
			name          string
			confFile      string
			metricEnabled bool
		}{
			{
				name:          "metric is enabled by default",
				confFile:      "./testdata/config.yaml",
				metricEnabled: true,
			},
			{
				name:          "metric is disabled by config",
				confFile:      "./testdata/metric_disabled.yaml",
				metricEnabled: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				config, err := LoadStandaloneConfiguration(tc.confFile)
				require.NoError(t, err)
				assert.Equal(t, tc.metricEnabled, config.Spec.MetricSpec.GetEnabled())
			})
		}
	})

	t.Run("components spec", func(t *testing.T) {
		testCases := []struct {
			name           string
			confFile       string
			componentsDeny []string
		}{
			{
				name:           "component deny list",
				confFile:       "./testdata/components_config.yaml",
				componentsDeny: []string{"foo.bar", "hello.world/v1"},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				config, err := LoadStandaloneConfiguration(tc.confFile)
				require.NoError(t, err)
				assert.True(t, reflect.DeepEqual(tc.componentsDeny, config.Spec.ComponentsSpec.Deny))
			})
		}
	})

	t.Run("features spec", func(t *testing.T) {
		testCases := []struct {
			name           string
			confFile       string
			featureName    Feature
			featureEnabled bool
		}{
			{
				name:           "feature is enabled",
				confFile:       "./testdata/feature_config.yaml",
				featureName:    Feature("Actor.Reentrancy"),
				featureEnabled: true,
			},
			{
				name:           "feature is disabled",
				confFile:       "./testdata/feature_config.yaml",
				featureName:    Feature("Test.Feature"),
				featureEnabled: false,
			},
			{
				name:           "feature is disabled if missing",
				confFile:       "./testdata/feature_config.yaml",
				featureName:    Feature("Test.Missing"),
				featureEnabled: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				config, err := LoadStandaloneConfiguration(tc.confFile)
				require.NoError(t, err)
				config.LoadFeatures()
				assert.Equal(t, tc.featureEnabled, config.IsFeatureEnabled(tc.featureName))
			})
		}
	})

	t.Run("mTLS spec", func(t *testing.T) {
		config, err := LoadStandaloneConfiguration("./testdata/mtls_config.yaml")
		require.NoError(t, err)
		mtlsSpec := config.GetMTLSSpec()
		assert.True(t, mtlsSpec.Enabled)
		assert.Equal(t, "25s", mtlsSpec.WorkloadCertTTL)
		assert.Equal(t, "1h", mtlsSpec.AllowedClockSkew)
	})

	t.Run("workflow spec - configured", func(t *testing.T) {
		config, err := LoadStandaloneConfiguration("./testdata/workflow_config.yaml")
		require.NoError(t, err)
		workflowSpec := config.GetWorkflowSpec()
		assert.Equal(t, int32(32), workflowSpec.MaxConcurrentWorkflowInvocations)
		assert.Equal(t, int32(64), workflowSpec.MaxConcurrentActivityInvocations)
	})

	t.Run("workflow spec - defaults", func(t *testing.T) {
		// Intentionally loading an unrelated config file to test defaults
		config, err := LoadStandaloneConfiguration("./testdata/mtls_config.yaml")
		require.NoError(t, err)
		workflowSpec := config.GetWorkflowSpec()

		// These are the documented default values. Changes to these defaults require changes to
		assert.Equal(t, int32(100), workflowSpec.MaxConcurrentWorkflowInvocations)
		assert.Equal(t, int32(100), workflowSpec.MaxConcurrentActivityInvocations)
	})

	t.Run("multiple configurations", func(t *testing.T) {
		config, err := LoadStandaloneConfiguration("./testdata/feature_config.yaml", "./testdata/mtls_config.yaml")
		require.NoError(t, err)

		// From feature_config.yaml
		config.LoadFeatures()
		assert.True(t, config.IsFeatureEnabled("Actor.Reentrancy"))
		assert.False(t, config.IsFeatureEnabled("Test.Feature"))

		// From mtls_config.yaml
		mtlsSpec := config.GetMTLSSpec()
		assert.True(t, mtlsSpec.Enabled)
		assert.Equal(t, "25s", mtlsSpec.WorkloadCertTTL)
		assert.Equal(t, "1h", mtlsSpec.AllowedClockSkew)
	})

	t.Run("multiple configurations with overriding", func(t *testing.T) {
		config, err := LoadStandaloneConfiguration("./testdata/feature_config.yaml", "./testdata/mtls_config.yaml", "./testdata/override.yaml")
		require.NoError(t, err)

		// From feature_config.yaml
		// Should both be overridden
		config.LoadFeatures()
		assert.False(t, config.IsFeatureEnabled("Actor.Reentrancy"))
		assert.True(t, config.IsFeatureEnabled("Test.Feature"))

		// From mtls_config.yaml
		mtlsSpec := config.GetMTLSSpec()
		assert.False(t, mtlsSpec.Enabled) // Overridden
		assert.Equal(t, "25s", mtlsSpec.WorkloadCertTTL)
		assert.Equal(t, "1h", mtlsSpec.AllowedClockSkew)

		// Spec part encoded as YAML
		compareWithFile(t, "./testdata/override_spec_gen.yaml", config.Spec.String())

		// Complete YAML
		compareWithFile(t, "./testdata/override_gen.yaml", config.String())
	})
}

func compareWithFile(t *testing.T, file string, expect string) {
	f, err := os.ReadFile(file)
	require.NoError(t, err)

	// Replace all "\r\n" with "\n" because (*wave hands*, *lesigh*) ... Windows
	f = bytes.ReplaceAll(f, []byte{'\r', '\n'}, []byte{'\n'})

	assert.Equal(t, expect, string(f))
}

func TestSortAndValidateSecretsConfigration(t *testing.T) {
	testCases := []struct {
		name          string
		config        Configuration
		errorExpected bool
	}{
		{
			name:          "empty configuration",
			errorExpected: false,
		},
		{
			name: "incorrect default access",
			config: Configuration{
				Spec: ConfigurationSpec{
					Secrets: &SecretsSpec{
						Scopes: []SecretsScope{
							{
								StoreName:     "testStore",
								DefaultAccess: "incorrect",
							},
						},
					},
				},
			},
			errorExpected: true,
		},
		{
			name: "empty default access",
			config: Configuration{
				Spec: ConfigurationSpec{
					Secrets: &SecretsSpec{
						Scopes: []SecretsScope{
							{
								StoreName: "testStore",
							},
						},
					},
				},
			},
			errorExpected: false,
		},
		{
			name: "repeated store Name",
			config: Configuration{
				Spec: ConfigurationSpec{
					Secrets: &SecretsSpec{
						Scopes: []SecretsScope{
							{
								StoreName:     "testStore",
								DefaultAccess: AllowAccess,
							},
							{
								StoreName:     "testStore",
								DefaultAccess: DenyAccess,
							},
						},
					},
				},
			},
			errorExpected: true,
		},
		{
			name: "simple secrets config",
			config: Configuration{
				Spec: ConfigurationSpec{
					Secrets: &SecretsSpec{
						Scopes: []SecretsScope{
							{
								StoreName:      "testStore",
								DefaultAccess:  DenyAccess,
								AllowedSecrets: []string{"Z", "b", "a", "c"},
							},
						},
					},
				},
			},
			errorExpected: false,
		},
		{
			name: "case-insensitive default access",
			config: Configuration{
				Spec: ConfigurationSpec{
					Secrets: &SecretsSpec{
						Scopes: []SecretsScope{
							{
								StoreName:      "testStore",
								DefaultAccess:  "DeNY",
								AllowedSecrets: []string{"Z", "b", "a", "c"},
							},
						},
					},
				},
			},
			errorExpected: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.sortAndValidateSecretsConfiguration()
			if tc.errorExpected {
				require.Error(t, err, "expected validation to fail")
			} else if tc.config.Spec.Secrets != nil {
				for _, scope := range tc.config.Spec.Secrets.Scopes {
					assert.True(t, sort.StringsAreSorted(scope.AllowedSecrets), "expected sorted slice")
					assert.True(t, sort.StringsAreSorted(scope.DeniedSecrets), "expected sorted slice")
				}
			}
		})
	}
}

func TestIsSecretAllowed(t *testing.T) {
	testCases := []struct {
		name           string
		scope          SecretsScope
		secretKey      string
		expectedResult bool
	}{
		{
			name:           "Empty scope default allow all",
			secretKey:      "random",
			expectedResult: true,
		},
		{
			name:           "Empty scope default allow all empty key",
			expectedResult: true,
		},
		{
			name: "default deny all secrets empty key",
			scope: SecretsScope{
				StoreName:     "testName",
				DefaultAccess: "DeNy", // check case-insensitivity
			},
			secretKey:      "",
			expectedResult: false,
		},
		{
			name: "default allow all secrets empty key",
			scope: SecretsScope{
				StoreName:     "testName",
				DefaultAccess: "AllOw", // check case-insensitivity
			},
			secretKey:      "",
			expectedResult: true,
		},
		{
			name: "default deny all secrets",
			scope: SecretsScope{
				StoreName:     "testName",
				DefaultAccess: DenyAccess,
			},
			secretKey:      "random",
			expectedResult: false,
		},
		{
			name: "default deny with specific allow secrets",
			scope: SecretsScope{
				StoreName:      "testName",
				DefaultAccess:  DenyAccess,
				AllowedSecrets: []string{"key1"},
			},
			secretKey:      "key1",
			expectedResult: true,
		},
		{
			name: "default allow with specific allow secrets",
			scope: SecretsScope{
				StoreName:      "testName",
				DefaultAccess:  AllowAccess,
				AllowedSecrets: []string{"key1"},
			},
			secretKey:      "key2",
			expectedResult: false,
		},
		{
			name: "default allow with specific deny secrets",
			scope: SecretsScope{
				StoreName:     "testName",
				DefaultAccess: AllowAccess,
				DeniedSecrets: []string{"key1"},
			},
			secretKey:      "key1",
			expectedResult: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedResult, tc.scope.IsSecretAllowed(tc.secretKey), "incorrect access")
		})
	}
}

func TestContainsKey(t *testing.T) {
	s := []string{"a", "b", "c", "z"}
	assert.False(t, containsKey(s, "h"), "unexpected result")
	assert.True(t, containsKey(s, "b"), "unexpected result")
}

func TestFeatureEnabled(t *testing.T) {
	config := Configuration{
		Spec: ConfigurationSpec{
			Features: []FeatureSpec{
				{
					Name:    "testEnabled",
					Enabled: true,
				},
				{
					Name:    "testDisabled",
					Enabled: false,
				},
			},
		},
	}
	config.LoadFeatures()

	assert.True(t, config.IsFeatureEnabled("testEnabled"))
	assert.False(t, config.IsFeatureEnabled("testDisabled"))
	assert.False(t, config.IsFeatureEnabled("testMissing"))

	// Test config.EnabledFeatures
	// We sort the values before comparing because order isn't guaranteed (and doesn't matter)
	actual := config.EnabledFeatures()
	expect := append([]string{"testEnabled"}, buildinfo.Features()...)
	sort.Strings(actual)
	sort.Strings(expect)
	assert.EqualValues(t, actual, expect)
}

func TestSetTracingSpecFromEnv(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otlpendpoint:1234")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/json")

	// get default configuration
	conf := LoadDefaultConfiguration()

	// set tracing spec from env
	SetTracingSpecFromEnv(conf)

	assert.Equal(t, "otlpendpoint:1234", conf.Spec.TracingSpec.Otel.EndpointAddress)
	assert.Equal(t, "http", conf.Spec.TracingSpec.Otel.Protocol)
	require.False(t, conf.Spec.TracingSpec.Otel.GetIsSecure())

	// Spec from config file should not be overridden
	conf = LoadDefaultConfiguration()
	conf.Spec.TracingSpec.Otel.EndpointAddress = "configfileendpoint:4321"
	conf.Spec.TracingSpec.Otel.Protocol = "grpc"
	conf.Spec.TracingSpec.Otel.IsSecure = ptr.Of(true)

	// set tracing spec from env
	SetTracingSpecFromEnv(conf)

	assert.Equal(t, "configfileendpoint:4321", conf.Spec.TracingSpec.Otel.EndpointAddress)
	assert.Equal(t, "grpc", conf.Spec.TracingSpec.Otel.Protocol)
	require.True(t, conf.Spec.TracingSpec.Otel.GetIsSecure())
}

func TestAPIAccessRules(t *testing.T) {
	config := &Configuration{
		Spec: ConfigurationSpec{
			APISpec: &APISpec{
				Allowed: APIAccessRules{
					APIAccessRule{Name: "foo", Version: "v1", Protocol: "http"},
					APIAccessRule{Name: "MyMethod", Version: "v1alpha1", Protocol: "grpc"},
				},
				Denied: APIAccessRules{
					APIAccessRule{Name: "bar", Version: "v1", Protocol: "http"},
				},
			},
		},
	}

	apiSpec := config.Spec.APISpec

	assert.Equal(t, []string{"v1/foo"}, maps.Keys(apiSpec.Allowed.GetRulesByProtocol(APIAccessRuleProtocolHTTP)))
	assert.Equal(t, []string{"v1alpha1/MyMethod"}, maps.Keys(apiSpec.Allowed.GetRulesByProtocol(APIAccessRuleProtocolGRPC)))
	assert.Equal(t, []string{"v1/bar"}, maps.Keys(apiSpec.Denied.GetRulesByProtocol(APIAccessRuleProtocolHTTP)))
	assert.Empty(t, maps.Keys(apiSpec.Denied.GetRulesByProtocol(APIAccessRuleProtocolGRPC)))
}

func TestSortMetrics(t *testing.T) {
	t.Run("metrics overrides metric - enabled false", func(t *testing.T) {
		config := &Configuration{
			Spec: ConfigurationSpec{
				MetricSpec: &MetricSpec{
					Enabled: ptr.Of(true),
					Rules: []MetricsRule{
						{
							Name: "rule",
						},
					},
				},
				MetricsSpec: &MetricSpec{
					Enabled: ptr.Of(false),
				},
			},
		}

		config.sortMetricsSpec()
		assert.False(t, config.Spec.MetricSpec.GetEnabled())
		assert.Equal(t, "rule", config.Spec.MetricSpec.Rules[0].Name)
	})

	t.Run("metrics overrides metric - enabled true", func(t *testing.T) {
		config := &Configuration{
			Spec: ConfigurationSpec{
				MetricSpec: &MetricSpec{
					Enabled: ptr.Of(false),
					Rules: []MetricsRule{
						{
							Name: "rule",
						},
					},
				},
				MetricsSpec: &MetricSpec{
					Enabled: ptr.Of(true),
				},
			},
		}

		config.sortMetricsSpec()
		assert.True(t, config.Spec.MetricSpec.GetEnabled())
		assert.Equal(t, "rule", config.Spec.MetricSpec.Rules[0].Name)
	})

	t.Run("nil metrics enabled doesn't overrides", func(t *testing.T) {
		config := &Configuration{
			Spec: ConfigurationSpec{
				MetricSpec: &MetricSpec{
					Enabled: ptr.Of(true),
					Rules: []MetricsRule{
						{
							Name: "rule",
						},
					},
				},
				MetricsSpec: &MetricSpec{},
			},
		}

		config.sortMetricsSpec()
		assert.True(t, config.Spec.MetricSpec.GetEnabled())
		assert.Equal(t, "rule", config.Spec.MetricSpec.Rules[0].Name)
	})
}

func TestMetricsGetHTTPIncreasedCardinality(t *testing.T) {
	log := logger.NewLogger("test")
	log.SetOutput(io.Discard)

	t.Run("no http configuration, returns false", func(t *testing.T) {
		m := MetricSpec{
			HTTP: nil,
		}
		assert.False(t, m.GetHTTPIncreasedCardinality())
	})

	t.Run("nil value, returns false", func(t *testing.T) {
		m := MetricSpec{
			HTTP: &MetricHTTP{
				IncreasedCardinality: nil,
			},
		}
		assert.False(t, m.GetHTTPIncreasedCardinality())
	})

	t.Run("value is set to true", func(t *testing.T) {
		m := MetricSpec{
			HTTP: &MetricHTTP{
				IncreasedCardinality: ptr.Of(true),
			},
		}
		assert.True(t, m.GetHTTPIncreasedCardinality())
	})

	t.Run("value is set to false", func(t *testing.T) {
		m := MetricSpec{
			HTTP: &MetricHTTP{
				IncreasedCardinality: ptr.Of(false),
			},
		}
		assert.False(t, m.GetHTTPIncreasedCardinality())
	})
}
