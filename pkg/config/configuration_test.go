// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package config

import (
	"sort"
	"testing"

	"github.com/dapr/dapr/pkg/proto/common/v1"
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

func initializeAccessControlList() (AccessControlList, error) {
	inputSpec := AccessControlSpec{
		DefaultAction: "deny",
		TrustDomain:   "public",
		AppPolicies: []AppPolicySpec{
			{
				AppName:       "app1",
				DefaultAction: "allow",
				TrustDomain:   "public",
				Namespace:     "ns1",
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
				TrustDomain:   "domain1",
				Namespace:     "ns2",
				AppOperationActions: []AppOperation{
					{
						Action:    "allow",
						HTTPVerb:  []string{"PUT", "GET"},
						Operation: "/op3/a/*",
					},
					{
						Action:    "allow",
						HTTPVerb:  []string{"POST"},
						Operation: "/op4",
					},
				},
			},
			{
				AppName:     "app3",
				TrustDomain: "domain1",
				Namespace:   "ns1",
				AppOperationActions: []AppOperation{
					{
						Action:    "allow",
						HTTPVerb:  []string{"POST"},
						Operation: "/op5",
					},
				},
			},
		},
	}
	accessControlList, err := ParseAccessControlSpec(inputSpec)

	return accessControlList, err
}

func TestParseAccessControlSpec(t *testing.T) {
	t.Run("translate to in-memory rules", func(t *testing.T) {
		accessControlList, err := initializeAccessControlList()

		assert.Nil(t, err)

		assert.Equal(t, "deny", accessControlList.DefaultAction)
		assert.Equal(t, "public", accessControlList.TrustDomain)

		// appName
		appName := "app1"
		assert.Equal(t, appName, accessControlList.PolicySpec[appName].AppName)
		assert.Equal(t, "allow", accessControlList.PolicySpec[appName].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[appName].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[appName].Namespace)

		op1Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op1Actions.VerbAction["POST"] = "allow"
		op1Actions.VerbAction["GET"] = "allow"

		op2Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op2Actions.VerbAction["*"] = "deny"
		assert.Equal(t, 2, len(accessControlList.PolicySpec[appName].AppOperationActions["/op1"].VerbAction))
		assert.Equal(t, op1Actions, accessControlList.PolicySpec[appName].AppOperationActions["/op1"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[appName].AppOperationActions["/op2"].VerbAction))
		assert.Equal(t, op2Actions, accessControlList.PolicySpec[appName].AppOperationActions["/op2"])

		// App2
		appName = "app2"
		assert.Equal(t, appName, accessControlList.PolicySpec[appName].AppName)
		assert.Equal(t, "deny", accessControlList.PolicySpec[appName].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[appName].TrustDomain)
		assert.Equal(t, "ns2", accessControlList.PolicySpec[appName].Namespace)

		op3Actions := AccessControlListOperationAction{
			OperationPostFix: "/a/*",
			VerbAction:       make(map[string]string),
		}
		op3Actions.VerbAction["PUT"] = "allow"
		op3Actions.VerbAction["GET"] = "allow"

		op4Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op4Actions.VerbAction["POST"] = "allow"
		assert.Equal(t, 2, len(accessControlList.PolicySpec[appName].AppOperationActions["/op3"].VerbAction))
		assert.Equal(t, op3Actions, accessControlList.PolicySpec[appName].AppOperationActions["/op3"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[appName].AppOperationActions["/op4"].VerbAction))
		assert.Equal(t, op4Actions, accessControlList.PolicySpec[appName].AppOperationActions["/op4"])

		// App3
		appName = "app3"
		assert.Equal(t, appName, accessControlList.PolicySpec[appName].AppName)
		assert.Equal(t, "", accessControlList.PolicySpec[appName].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[appName].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[appName].Namespace)

		op5Actions := AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op5Actions.VerbAction["POST"] = "allow"

		assert.Equal(t, 1, len(accessControlList.PolicySpec[appName].AppOperationActions["/op5"].VerbAction))
		assert.Equal(t, op5Actions, accessControlList.PolicySpec[appName].AppOperationActions["/op5"])
	})

	t.Run("test when no trust domain and namespace specified in app policy", func(t *testing.T) {
		invalidAccessControlSpec := AccessControlSpec{
			DefaultAction: "deny",
			TrustDomain:   "public",
			AppPolicies: []AppPolicySpec{
				{
					AppName:       "app1",
					DefaultAction: "allow",
					Namespace:     "ns1",
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
					TrustDomain:   "domain1",
					AppOperationActions: []AppOperation{
						{
							Action:    "allow",
							HTTPVerb:  []string{"PUT", "GET"},
							Operation: "/op3/a/*",
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

		_, err := ParseAccessControlSpec(invalidAccessControlSpec)
		assert.Error(t, err, "Invalid Access Control Spec. Missing TrustDomain for apps: [app1], Missing Namespace for apps: [app2]")
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

	t.Run("test empty spiffe id", func(t *testing.T) {
		spiffeID := ""
		_, err := parseSpiffeID(spiffeID)
		assert.NotNil(t, err)
	})
}

func TestIsOperationAllowedByAccessControlPolicy(t *testing.T) {
	t.Run("test when no acl specified", func(t *testing.T) {
		srcAppID := "app1"
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, nil)
		// Action = Allow the operation since no ACL is defined
		assert.True(t, isAllowed)
	})

	t.Run("test when no matching app in acl found", func(t *testing.T) {
		srcAppID := "app3"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain does not match", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "private",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when namespace does not match", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "abcd",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when spiffe id is nil", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList, _ := initializeAccessControlList()
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when src app id is empty", func(t *testing.T) {
		srcAppID := ""
		accessControlList, _ := initializeAccessControlList()
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, &accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb is not found", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific app
		assert.False(t, isAllowed)
	})

	t.Run("test when default action for app is not specified and no matching http verb found", func(t *testing.T) {
		srcAppID := "app3"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op5", common.HTTPExtension_PUT, &accessControlList)
		// Action = Global Default action
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches *", func(t *testing.T) {
		srcAppID := "app1"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op2", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches a specific verb", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_POST, &accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := "app2"
		accessControlList, _ := initializeAccessControlList()
		spiffeID := SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a/b", common.HTTPExtension_PUT, &accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
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
}
