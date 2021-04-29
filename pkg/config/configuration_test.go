// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
	"os"
	"sort"
	"testing"

	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/stretchr/testify/assert"
)

const (
	app1    = "app1"
	app2    = "app2"
	app3    = "app3"
	app1Ns1 = "app1||ns1"
	app2Ns2 = "app2||ns2"
	app3Ns1 = "app3||ns1"
	app1Ns4 = "app1||ns4"
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

func initializeAccessControlList(protocol string) (*AccessControlList, error) {
	inputSpec := AccessControlSpec{
		DefaultAction: DenyAccess,
		TrustDomain:   "abcd",
		AppPolicies: []AppPolicySpec{
			{
				AppName:       app1,
				DefaultAction: AllowAccess,
				TrustDomain:   "public",
				Namespace:     "ns1",
				AppOperationActions: []AppOperation{
					{
						Action:    AllowAccess,
						HTTPVerb:  []string{"POST", "GET"},
						Operation: "/op1",
					},
					{
						Action:    DenyAccess,
						HTTPVerb:  []string{"*"},
						Operation: "/op2",
					},
				},
			},
			{
				AppName:       app2,
				DefaultAction: DenyAccess,
				TrustDomain:   "domain1",
				Namespace:     "ns2",
				AppOperationActions: []AppOperation{
					{
						Action:    AllowAccess,
						HTTPVerb:  []string{"PUT", "GET"},
						Operation: "/op3/a/*",
					},
					{
						Action:    AllowAccess,
						HTTPVerb:  []string{"POST"},
						Operation: "/op4",
					},
				},
			},
			{
				AppName:     app3,
				TrustDomain: "domain1",
				Namespace:   "ns1",
				AppOperationActions: []AppOperation{
					{
						Action:    AllowAccess,
						HTTPVerb:  []string{"POST"},
						Operation: "/op5",
					},
				},
			},
			{
				AppName:       app1, // Duplicate app id with a different namespace
				DefaultAction: AllowAccess,
				TrustDomain:   "public",
				Namespace:     "ns4",
				AppOperationActions: []AppOperation{
					{
						Action:    AllowAccess,
						HTTPVerb:  []string{"*"},
						Operation: "/op6",
					},
				},
			},
		},
	}
	accessControlList, err := ParseAccessControlSpec(inputSpec, protocol)

	return accessControlList, err
}

func TestParseAccessControlSpec(t *testing.T) {
	t.Run("translate to in-memory rules", func(t *testing.T) {
		accessControlList, err := initializeAccessControlList(HTTPProtocol)

		assert.Nil(t, err)

		assert.Equal(t, DenyAccess, accessControlList.DefaultAction)
		assert.Equal(t, "abcd", accessControlList.TrustDomain)

		// App1
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns1].AppName)
		assert.Equal(t, AllowAccess, accessControlList.PolicySpec[app1Ns1].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app1Ns1].Namespace)

		op1Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op1Actions.VerbAction["POST"] = AllowAccess
		op1Actions.VerbAction["GET"] = AllowAccess
		op1Actions.OperationAction = AllowAccess

		op2Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op2Actions.VerbAction["*"] = DenyAccess
		op2Actions.OperationAction = DenyAccess

		assert.Equal(t, 2, len(accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op1"].VerbAction))
		assert.Equal(t, op1Actions, accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op1"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op2"].VerbAction))
		assert.Equal(t, op2Actions, accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op2"])

		// App2
		assert.Equal(t, app2, accessControlList.PolicySpec[app2Ns2].AppName)
		assert.Equal(t, DenyAccess, accessControlList.PolicySpec[app2Ns2].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app2Ns2].TrustDomain)
		assert.Equal(t, "ns2", accessControlList.PolicySpec[app2Ns2].Namespace)

		op3Actions := AccessControlListOperationAction{
			OperationPostFix: "/a/*",
			VerbAction:       make(map[string]string),
		}
		op3Actions.VerbAction["PUT"] = AllowAccess
		op3Actions.VerbAction["GET"] = AllowAccess
		op3Actions.OperationAction = AllowAccess

		op4Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op4Actions.VerbAction["POST"] = AllowAccess
		op4Actions.OperationAction = AllowAccess

		assert.Equal(t, 2, len(accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op3"].VerbAction))
		assert.Equal(t, op3Actions, accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op3"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op4"].VerbAction))
		assert.Equal(t, op4Actions, accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op4"])

		// App3
		assert.Equal(t, app3, accessControlList.PolicySpec[app3Ns1].AppName)
		assert.Equal(t, "", accessControlList.PolicySpec[app3Ns1].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app3Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app3Ns1].Namespace)

		op5Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op5Actions.VerbAction["POST"] = AllowAccess
		op5Actions.OperationAction = AllowAccess

		assert.Equal(t, 1, len(accessControlList.PolicySpec[app3Ns1].AppOperationActions["/op5"].VerbAction))
		assert.Equal(t, op5Actions, accessControlList.PolicySpec[app3Ns1].AppOperationActions["/op5"])

		// App1 with a different namespace
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns4].AppName)
		assert.Equal(t, AllowAccess, accessControlList.PolicySpec[app1Ns4].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns4].TrustDomain)
		assert.Equal(t, "ns4", accessControlList.PolicySpec[app1Ns4].Namespace)

		op6Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op6Actions.VerbAction["*"] = AllowAccess
		op6Actions.OperationAction = AllowAccess

		assert.Equal(t, 1, len(accessControlList.PolicySpec[app1Ns4].AppOperationActions["/op6"].VerbAction))
		assert.Equal(t, op6Actions, accessControlList.PolicySpec[app1Ns4].AppOperationActions["/op6"])
	})

	t.Run("test when no trust domain and namespace specified in app policy", func(t *testing.T) {
		invalidAccessControlSpec := AccessControlSpec{
			DefaultAction: DenyAccess,
			TrustDomain:   "public",
			AppPolicies: []AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: AllowAccess,
					Namespace:     "ns1",
					AppOperationActions: []AppOperation{
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
				{
					AppName:       app2,
					DefaultAction: DenyAccess,
					TrustDomain:   "domain1",
					AppOperationActions: []AppOperation{
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"PUT", "GET"},
							Operation: "/op3/a/*",
						},
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"POST"},
							Operation: "/op4",
						},
					},
				},
				{
					AppName:       "",
					DefaultAction: DenyAccess,
					TrustDomain:   "domain1",
					AppOperationActions: []AppOperation{
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"PUT", "GET"},
							Operation: "/op3/a/*",
						},
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"POST"},
							Operation: "/op4",
						},
					},
				},
			},
		}

		_, err := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Error(t, err, "invalid access control spec. missing trustdomain for apps: [%s], missing namespace for apps: [%s], missing app name on at least one of the app policies: true", app1, app2)
	})

	t.Run("test when no trust domain is specified for the app", func(t *testing.T) {
		accessControlSpec := AccessControlSpec{
			DefaultAction: DenyAccess,
			TrustDomain:   "",
			AppPolicies: []AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: AllowAccess,
					TrustDomain:   "public",
					Namespace:     "ns1",
					AppOperationActions: []AppOperation{
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
			},
		}

		accessControlList, _ := ParseAccessControlSpec(accessControlSpec, "http")
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
	})

	t.Run("test when no access control policy has been specified", func(t *testing.T) {
		invalidAccessControlSpec := AccessControlSpec{
			DefaultAction: "",
			TrustDomain:   "",
			AppPolicies:   []AppPolicySpec{},
		}

		accessControlList, _ := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Nil(t, accessControlList)
	})

	t.Run("test when no default global action has been specified", func(t *testing.T) {
		invalidAccessControlSpec := AccessControlSpec{
			TrustDomain: "public",
			AppPolicies: []AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: AllowAccess,
					TrustDomain:   "domain1",
					Namespace:     "ns1",
					AppOperationActions: []AppOperation{
						{
							Action:    AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
			},
		}

		accessControlList, _ := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Equal(t, accessControlList.DefaultAction, DenyAccess)
	})
}

func TestSpiffeID(t *testing.T) {
	t.Run("test parse spiffe id", func(t *testing.T) {
		spiffeID := "spiffe://mydomain/ns/mynamespace/myappid"
		id, err := parseSpiffeID(spiffeID)
		assert.Equal(t, "mydomain", id.TrustDomain)
		assert.Equal(t, "mynamespace", id.Namespace)
		assert.Equal(t, "myappid", id.AppID)
		assert.Nil(t, err)
	})

	t.Run("test parse invalid spiffe id", func(t *testing.T) {
		spiffeID := "abcd"
		_, err := parseSpiffeID(spiffeID)
		assert.NotNil(t, err)
	})

	t.Run("test parse spiffe id with not all fields", func(t *testing.T) {
		spiffeID := "spiffe://mydomain/ns/myappid"
		_, err := parseSpiffeID(spiffeID)
		assert.NotNil(t, err)
	})

	t.Run("test empty spiffe id", func(t *testing.T) {
		spiffeID := ""
		_, err := parseSpiffeID(spiffeID)
		assert.NotNil(t, err)
	})
}

func TestIsOperationAllowedByAccessControlPolicy(t *testing.T) {
	t.Run("test when no acl specified", func(t *testing.T) {
		srcAppID := app1
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, nil)
		// Action = Allow the operation since no ACL is defined
		assert.True(t, isAllowed)
	})

	t.Run("test when no matching app in acl found", func(t *testing.T) {
		srcAppID := "appX"
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "private",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when namespace does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "abcd",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when spiffe id is nil", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when src app id is empty", func(t *testing.T) {
		srcAppID := ""
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when operation is not found in the policy spec", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "opX", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.True(t, isAllowed)
	})

	t.Run("test http case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "Op2", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb is not found", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Default action for the specific app
		assert.False(t, isAllowed)
	})

	t.Run("test when default action for app is not specified and no matching http verb found", func(t *testing.T) {
		srcAppID := app3
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op5", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Global Default action
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches *", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op2", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches a specific verb", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when operation is invoked with /", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op4", common.HTTPExtension_POST, HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when http verb is not specified", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_NONE, HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test grpc case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(GRPCProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/OP4", common.HTTPExtension_NONE, GRPCProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/b/b", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(HTTPProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a/b", common.HTTPExtension_PUT, HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})

	t.Run("test with grpc invocation", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(GRPCProtocol)
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_NONE, GRPCProtocol, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})
}

func TestGetOperationPrefixAndPostfix(t *testing.T) {
	t.Run("test when operation single post fix exists", func(t *testing.T) {
		operation := "/invoke/*"
		prefix, postfix := getOperationPrefixAndPostfix(operation)
		assert.Equal(t, "/invoke", prefix)
		assert.Equal(t, "/*", postfix)
	})

	t.Run("test when operation longer post fix exists", func(t *testing.T) {
		operation := "/invoke/a/*"
		prefix, postfix := getOperationPrefixAndPostfix(operation)
		assert.Equal(t, "/invoke", prefix)
		assert.Equal(t, "/a/*", postfix)
	})

	t.Run("test when operation no post fix exists", func(t *testing.T) {
		operation := "/invoke"
		prefix, postfix := getOperationPrefixAndPostfix(operation)
		assert.Equal(t, "/invoke", prefix)
		assert.Equal(t, "/", postfix)
	})

	t.Run("test operation multi path post fix exists", func(t *testing.T) {
		operation := "/invoke/a/b/*"
		prefix, postfix := getOperationPrefixAndPostfix(operation)
		assert.Equal(t, "/invoke", prefix)
		assert.Equal(t, "/a/b/*", postfix)
	})
}
