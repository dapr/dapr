// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
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
			config, _, err := LoadStandaloneConfiguration(tc.path)
			if tc.errorExpected {
				assert.Error(t, err, "Expected an error")
				assert.Nil(t, config, "Config should not be loaded")
			} else {
				assert.NoError(t, err, "Unexpected error")
				assert.NotNil(t, config, "Config not loaded as expected")
			}
		})
	}

	t.Run("Parse environment variables", func(t *testing.T) {
		os.Setenv("DAPR_SECRET", "keepitsecret")
		config, _, err := LoadStandaloneConfiguration("./testdata/env_variables_config.yaml")
		assert.NoError(t, err, "Unexpected error")
		assert.NotNil(t, config, "Config not loaded as expected")
		assert.Equal(t, "keepitsecret", config.Spec.Secrets.Scopes[0].AllowedSecrets[0])
	})
}

func TestLoadStandaloneConfigurationKindName(t *testing.T) {
	t.Run("test Kind and Name", func(t *testing.T) {
		config, _, err := LoadStandaloneConfiguration("./testdata/config.yaml")
		assert.NoError(t, err, "Unexpected error")
		assert.NotNil(t, config, "Config not loaded as expected")
		assert.Equal(t, "secretappconfig", config.ObjectMeta.Name)
		assert.Equal(t, "Configuration", config.TypeMeta.Kind)
	})
}

func TestMetricSpecForStandAlone(t *testing.T) {
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
			config, _, err := LoadStandaloneConfiguration(tc.confFile)
			assert.NoError(t, err)
			assert.Equal(t, tc.metricEnabled, config.Spec.MetricSpec.Enabled)
		})
	}
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
					Secrets: SecretsSpec{
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
					Secrets: SecretsSpec{
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
					Secrets: SecretsSpec{
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
					Secrets: SecretsSpec{
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
					Secrets: SecretsSpec{
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
			err := sortAndValidateSecretsConfiguration(&tc.config)
			if tc.errorExpected {
				assert.Error(t, err, "expected validation to fail")
			} else {
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
			assert.Equal(t, tc.scope.IsSecretAllowed(tc.secretKey), tc.expectedResult, "incorrect access")
		})
	}
}

func TestContainsKey(t *testing.T) {
	s := []string{"a", "b", "c", "z"}
	assert.False(t, containsKey(s, "h"), "unexpected result")
	assert.True(t, containsKey(s, "b"), "unexpected result")
}

func TestFeatureEnabled(t *testing.T) {
	t.Run("Test feature enabled is correct", func(t *testing.T) {
		features := []FeatureSpec{
			{
				Name:    "testEnabled",
				Enabled: true,
			},
			{
				Name:    "testDisabled",
				Enabled: false,
			},
		}
		assert.True(t, IsFeatureEnabled(features, "testEnabled"))
		assert.False(t, IsFeatureEnabled(features, "testDisabled"))
		assert.False(t, IsFeatureEnabled(features, "testMissing"))
	})
}

func TestFeatureSpecForStandAlone(t *testing.T) {
	testCases := []struct {
		name           string
		confFile       string
		featureName    Feature
		featureEnabled bool
	}{
		{
			name:           "Feature is enabled",
			confFile:       "./testdata/feature_config.yaml",
			featureName:    Feature("Actor.Reentrancy"),
			featureEnabled: true,
		},
		{
			name:           "Feature is disabled",
			confFile:       "./testdata/feature_config.yaml",
			featureName:    Feature("Test.Feature"),
			featureEnabled: false,
		},
		{
			name:           "Feature is disabled if missing",
			confFile:       "./testdata/feature_config.yaml",
			featureName:    Feature("Test.Missing"),
			featureEnabled: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, _, err := LoadStandaloneConfiguration(tc.confFile)
			assert.NoError(t, err)
			assert.Equal(t, tc.featureEnabled, IsFeatureEnabled(config.Spec.Features, tc.featureName))
		})
	}
}

func TestMTLSSpecForStandAlone(t *testing.T) {
	t.Run("test mtls spec config", func(t *testing.T) {
		config, _, err := LoadStandaloneConfiguration("./testdata/mtls_config.yaml")
		assert.NoError(t, err)
		assert.True(t, config.Spec.MTLSSpec.Enabled)
		assert.Equal(t, "25s", config.Spec.MTLSSpec.WorkloadCertTTL)
		assert.Equal(t, "1h", config.Spec.MTLSSpec.AllowedClockSkew)
	})
}
