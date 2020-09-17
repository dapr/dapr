// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
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
			config, err := LoadStandaloneConfiguration(tc.path)
			if tc.errorExpected {
				assert.Error(t, err, "Expected an error")
				assert.Nil(t, config, "Config should not be loaded")
			} else {
				assert.NoError(t, err, "Unexpected error")
				assert.NotNil(t, config, "Config not loaded as expected")
			}
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
								DefaultAccess: "allow",
							},
							{
								StoreName:     "testStore",
								DefaultAccess: "deny",
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
								DefaultAccess:  "deny",
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
				DefaultAccess: "deny",
			},
			secretKey:      "random",
			expectedResult: false,
		},
		{
			name: "default deny with specific allow secrets",
			scope: SecretsScope{
				StoreName:      "testName",
				DefaultAccess:  "deny",
				AllowedSecrets: []string{"key1"},
			},
			secretKey:      "key1",
			expectedResult: true,
		},
		{
			name: "default allow with specific allow secrets",
			scope: SecretsScope{
				StoreName:      "testName",
				DefaultAccess:  "allow",
				AllowedSecrets: []string{"key1"},
			},
			secretKey:      "key2",
			expectedResult: false,
		},
		{
			name: "default allow with specific deny secrets",
			scope: SecretsScope{
				StoreName:     "testName",
				DefaultAccess: "allow",
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

func initializeAccessControlList() AccessControlList {
	inputSpec := AccessControlSpec{
		DefaultAction: "deny",
		AppPolicies: []AppPolicySpec{
			{
				AppName:       "app1",
				DefaultAction: "allow",
				TrustDomain:   "public",
				AppOperationActions: []AppOperation{
					{
						Action:    "allow",
						HTTPVerb:  []string{"POST", "GET"},
						Operation: "/op1",
					},
					{
						Action:    "deny",
						HTTPVerb:  []string{"*"},
						Operation: "/op2",
					},
				},
			},
			{
				AppName:       "app2",
				DefaultAction: "deny",
				TrustDomain:   "*",
				AppOperationActions: []AppOperation{
					{
						Action:    "deny",
						HTTPVerb:  []string{"PUT", "GET"},
						Operation: "/op3",
					},
					{
						Action:    "allow",
						HTTPVerb:  []string{"POST"},
						Operation: "/op4",
					},
				},
			},
		},
	}
	accessControlList := TranslateAccessControlSpec(inputSpec)

	return accessControlList
}

func TestTranslateAccessControlSpec(t *testing.T) {
	t.Run("translate to in-memory rules", func(t *testing.T) {
		accessControlList := initializeAccessControlList()

		assert.Equal(t, "deny", accessControlList.DefaultAction)

		// appName
		appName := "app1"
		assert.Equal(t, appName, accessControlList.PolicySpec[appName].AppName)
		assert.Equal(t, "allow", accessControlList.PolicySpec[appName].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[appName].TrustDomain)
		assert.Equal(t, "/op1", accessControlList.PolicySpec[appName].AppOperationActions[0].Operation)
		assert.Equal(t, "allow", accessControlList.PolicySpec[appName].AppOperationActions[0].Action)
		assert.Equal(t, []string([]string{"POST", "GET"}), accessControlList.PolicySpec[appName].AppOperationActions[0].HTTPVerb)
		assert.Equal(t, "/op2", accessControlList.PolicySpec[appName].AppOperationActions[1].Operation)
		assert.Equal(t, "deny", accessControlList.PolicySpec[appName].AppOperationActions[1].Action)
		assert.Equal(t, []string([]string{"*"}), accessControlList.PolicySpec[appName].AppOperationActions[1].HTTPVerb)

		// App2
		appName = "app2"
		assert.Equal(t, appName, accessControlList.PolicySpec[appName].AppName)
		assert.Equal(t, "deny", accessControlList.PolicySpec[appName].DefaultAction)
		assert.Equal(t, "*", accessControlList.PolicySpec[appName].TrustDomain)
		assert.Equal(t, "/op3", accessControlList.PolicySpec[appName].AppOperationActions[0].Operation)
		assert.Equal(t, "deny", accessControlList.PolicySpec[appName].AppOperationActions[0].Action)
		assert.Equal(t, []string([]string{"PUT", "GET"}), accessControlList.PolicySpec[appName].AppOperationActions[0].HTTPVerb)
		assert.Equal(t, "/op4", accessControlList.PolicySpec[appName].AppOperationActions[1].Operation)
		assert.Equal(t, "allow", accessControlList.PolicySpec[appName].AppOperationActions[1].Action)
		assert.Equal(t, []string([]string{"POST"}), accessControlList.PolicySpec[appName].AppOperationActions[1].HTTPVerb)
	})
}

func TestSpiffeID(t *testing.T) {
	t.Run("test parse spiffe id", func(t *testing.T) {
		spiffeID := "spiffe://mydomain/ns/mynamespace/myappid"
		id := parseSpiffeID(spiffeID)
		assert.Equal(t, "mydomain", id.trustDomain)
		assert.Equal(t, "mynamespace", id.namespace)
		assert.Equal(t, "myappid", id.appID)
	})
}

func TestIsOperationAllowedByAccessControlPolicy(t *testing.T) {
	t.Run("test when no acl specified", func(t *testing.T) {
		srcAppID := "app1"
		spiffeID := SpiffeID{
			trustDomain: "*",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, nil)
		// Action = Allow the operation since no ACL is defined
		assert.True(t, isAllowed)
	})

	t.Run("test when no matching app in acl found", func(t *testing.T) {
		srcAppID := "app3"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "*",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain does not match", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "private",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default action for the specific app
		assert.True(t, isAllowed)
	})

	t.Run("test when spiffe id is nil", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList := initializeAccessControlList()
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when src app id is empty", func(t *testing.T) {
		srcAppID := ""
		accessControlList := initializeAccessControlList()
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain matches *", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "private",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_POST, &accessControlList)
		// Action = Action for the specific verb in the app. If verb not found, default action for the specific app
		assert.True(t, isAllowed)
	})

	t.Run("test when http verb is not found", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "private",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific app
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches *", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "public",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op2", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches a specific verb", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList := initializeAccessControlList()
		spiffeID := SpiffeID{
			trustDomain: "public",
			namespace:   "ns1",
			appID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op3", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})
}
