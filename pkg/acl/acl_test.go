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

//nolint:nosnakecase
package acl

import (
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/security/spiffe"
)

const (
	app1    = "app1"
	app2    = "app2"
	app3    = "app3"
	app4    = "app4"
	app1Ns1 = "app1||ns1"
	app2Ns2 = "app2||ns2"
	app3Ns1 = "app3||ns1"
	app1Ns4 = "app1||ns4"
)

func initializeAccessControlList(isHTTP bool) (*config.AccessControlList, error) {
	inputSpec := config.AccessControlSpec{
		DefaultAction: config.DenyAccess,
		TrustDomain:   "abcd",
		AppPolicies: []config.AppPolicySpec{
			{
				AppName:       app1,
				DefaultAction: config.AllowAccess,
				TrustDomain:   "public",
				Namespace:     "ns1",
				AppOperationActions: []config.AppOperation{
					{
						Action:    config.AllowAccess,
						HTTPVerb:  []string{"POST", "GET"},
						Operation: "/op1",
					},
					{
						Action:    config.DenyAccess,
						HTTPVerb:  []string{"*"},
						Operation: "/op2",
					},
				},
			},
			{
				AppName:       app2,
				DefaultAction: config.DenyAccess,
				TrustDomain:   "domain1",
				Namespace:     "ns2",
				AppOperationActions: []config.AppOperation{
					{
						Action:    config.AllowAccess,
						HTTPVerb:  []string{"PUT", "GET"},
						Operation: "/op3/a/*",
					},
					{
						Action:    config.AllowAccess,
						HTTPVerb:  []string{"POST"},
						Operation: "/op4",
					},
				},
			},
			{
				AppName:     app3,
				TrustDomain: "domain1",
				Namespace:   "ns1",
				AppOperationActions: []config.AppOperation{
					{
						Action:    config.AllowAccess,
						HTTPVerb:  []string{"POST"},
						Operation: "/op5",
					},
				},
			},
			{
				AppName:       app4,
				DefaultAction: config.DenyAccess,
				TrustDomain:   "domain1",
				Namespace:     "ns2",
				AppOperationActions: []config.AppOperation{
					{
						Action:    config.AllowAccess,
						Operation: "/op6",
					},
					{
						Action:    config.AllowAccess,
						Operation: "/op7/a/b/*",
					},
					{
						Action:    config.AllowAccess,
						Operation: "/op7/a/b/c/f",
					},
					{
						Action:    config.AllowAccess,
						Operation: "/op7/a/b*",
					},
					{
						Action:    config.AllowAccess,
						Operation: "/op7/c/**",
					},
				},
			},
			{
				AppName:       app1, // Duplicate app id with a different namespace
				DefaultAction: config.AllowAccess,
				TrustDomain:   "public",
				Namespace:     "ns4",
				AppOperationActions: []config.AppOperation{
					{
						Action:    config.AllowAccess,
						HTTPVerb:  []string{"*"},
						Operation: "/op6",
					},
				},
			},
		},
	}
	accessControlList, err := ParseAccessControlSpec(&inputSpec, isHTTP)

	return accessControlList, err
}

func TestParseAccessControlSpec(t *testing.T) {
	t.Run("translate to in-memory rules", func(t *testing.T) {
		accessControlList, err := initializeAccessControlList(true)

		require.NoError(t, err)

		assert.Equal(t, config.DenyAccess, accessControlList.DefaultAction)
		assert.Equal(t, "abcd", accessControlList.TrustDomain)

		// App1
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns1].AppName)
		assert.Equal(t, config.AllowAccess, accessControlList.PolicySpec[app1Ns1].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app1Ns1].Namespace)

		op1Actions := config.AccessControlListOperationAction{
			OperationName: "/op1",
			VerbAction:    make(map[string]string),
		}
		op1Actions.VerbAction["POST"] = config.AllowAccess
		op1Actions.VerbAction["GET"] = config.AllowAccess
		op1Actions.OperationAction = config.AllowAccess

		op2Actions := config.AccessControlListOperationAction{
			OperationName: "/op2",
			VerbAction:    make(map[string]string),
		}
		op2Actions.VerbAction["*"] = config.DenyAccess
		op2Actions.OperationAction = config.DenyAccess

		assert.Equal(t, op1Actions.VerbAction, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op1").VerbAction)
		assert.Equal(t, op1Actions.OperationAction, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op1").OperationAction)
		assert.Equal(t, op1Actions.OperationName, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op1").OperationName)
		assert.Equal(t, op2Actions.VerbAction, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op2").VerbAction)
		assert.Equal(t, op2Actions.OperationAction, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op2").OperationAction)
		assert.Equal(t, op2Actions.OperationName, accessControlList.PolicySpec[app1Ns1].AppOperationActions.Search("/op2").OperationName)

		// App2
		assert.Equal(t, app2, accessControlList.PolicySpec[app2Ns2].AppName)
		assert.Equal(t, config.DenyAccess, accessControlList.PolicySpec[app2Ns2].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app2Ns2].TrustDomain)
		assert.Equal(t, "ns2", accessControlList.PolicySpec[app2Ns2].Namespace)

		op3Actions := config.AccessControlListOperationAction{
			OperationName: "/op3/a/*",
			VerbAction:    make(map[string]string),
		}
		op3Actions.VerbAction["PUT"] = config.AllowAccess
		op3Actions.VerbAction["GET"] = config.AllowAccess
		op3Actions.OperationAction = config.AllowAccess

		op4Actions := config.AccessControlListOperationAction{
			OperationName: "/op4",
			VerbAction:    make(map[string]string),
		}
		op4Actions.VerbAction["POST"] = config.AllowAccess
		op4Actions.OperationAction = config.AllowAccess

		assert.Equal(t, op3Actions.VerbAction, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op3/a/*").VerbAction)
		assert.Equal(t, op3Actions.OperationName, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op3/a/*").OperationName)
		assert.Equal(t, op3Actions.OperationAction, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op3/a/*").OperationAction)

		assert.Equal(t, op4Actions.VerbAction, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op4").VerbAction)
		assert.Equal(t, op4Actions.OperationName, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op4").OperationName)
		assert.Equal(t, op4Actions.OperationAction, accessControlList.PolicySpec[app2Ns2].AppOperationActions.Search("/op4").OperationAction)

		// App3
		assert.Equal(t, app3, accessControlList.PolicySpec[app3Ns1].AppName)
		assert.Empty(t, accessControlList.PolicySpec[app3Ns1].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app3Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app3Ns1].Namespace)

		op5Actions := config.AccessControlListOperationAction{
			OperationName: "/op5",
			VerbAction:    make(map[string]string),
		}
		op5Actions.VerbAction["POST"] = config.AllowAccess
		op5Actions.OperationAction = config.AllowAccess

		assert.Equal(t, op5Actions.VerbAction, accessControlList.PolicySpec[app3Ns1].AppOperationActions.Search("/op5").VerbAction)
		assert.Equal(t, op5Actions.OperationName, accessControlList.PolicySpec[app3Ns1].AppOperationActions.Search("/op5").OperationName)
		assert.Equal(t, op5Actions.OperationAction, accessControlList.PolicySpec[app3Ns1].AppOperationActions.Search("/op5").OperationAction)

		// App1 with a different namespace
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns4].AppName)
		assert.Equal(t, config.AllowAccess, accessControlList.PolicySpec[app1Ns4].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns4].TrustDomain)
		assert.Equal(t, "ns4", accessControlList.PolicySpec[app1Ns4].Namespace)

		op6Actions := config.AccessControlListOperationAction{
			OperationName: "/op6",
			VerbAction:    make(map[string]string),
		}
		op6Actions.VerbAction["*"] = config.AllowAccess
		op6Actions.OperationAction = config.AllowAccess

		assert.Equal(t, op6Actions.VerbAction, accessControlList.PolicySpec[app1Ns4].AppOperationActions.Search("/op6").VerbAction)
		assert.Equal(t, op6Actions.OperationName, accessControlList.PolicySpec[app1Ns4].AppOperationActions.Search("/op6").OperationName)
		assert.Equal(t, op6Actions.OperationAction, accessControlList.PolicySpec[app1Ns4].AppOperationActions.Search("/op6").OperationAction)
	})

	t.Run("test when no trust domain and namespace specified in app policy", func(t *testing.T) {
		invalidAccessControlSpec := &config.AccessControlSpec{
			DefaultAction: config.DenyAccess,
			TrustDomain:   "public",
			AppPolicies: []config.AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: config.AllowAccess,
					Namespace:     "ns1",
					AppOperationActions: []config.AppOperation{
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    config.DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
				{
					AppName:       app2,
					DefaultAction: config.DenyAccess,
					TrustDomain:   "domain1",
					AppOperationActions: []config.AppOperation{
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"PUT", "GET"},
							Operation: "/op3/a/*",
						},
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"POST"},
							Operation: "/op4",
						},
					},
				},
				{
					AppName:       "",
					DefaultAction: config.DenyAccess,
					TrustDomain:   "domain1",
					AppOperationActions: []config.AppOperation{
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"PUT", "GET"},
							Operation: "/op3/a/*",
						},
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"POST"},
							Operation: "/op4",
						},
					},
				},
			},
		}

		_, err := ParseAccessControlSpec(invalidAccessControlSpec, true)
		require.Error(t, err, "invalid access control spec. missing trustdomain for apps: [%s], missing namespace for apps: [%s], missing app name on at least one of the app policies: true", app1, app2)
	})

	t.Run("test when no trust domain is specified for the app", func(t *testing.T) {
		accessControlSpec := &config.AccessControlSpec{
			DefaultAction: config.DenyAccess,
			TrustDomain:   "",
			AppPolicies: []config.AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: config.AllowAccess,
					TrustDomain:   "public",
					Namespace:     "ns1",
					AppOperationActions: []config.AppOperation{
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    config.DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
			},
		}

		accessControlList, _ := ParseAccessControlSpec(accessControlSpec, true)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
	})

	t.Run("test when no access control policy has been specified", func(t *testing.T) {
		accessControlList, err := ParseAccessControlSpec(nil, true)
		require.NoError(t, err)
		assert.Nil(t, accessControlList)

		invalidAccessControlSpec := &config.AccessControlSpec{
			DefaultAction: "",
			TrustDomain:   "",
			AppPolicies:   []config.AppPolicySpec{},
		}

		accessControlList, _ = ParseAccessControlSpec(invalidAccessControlSpec, true)
		require.NoError(t, err)
		assert.Nil(t, accessControlList)
	})

	t.Run("test when no default global action has been specified", func(t *testing.T) {
		invalidAccessControlSpec := &config.AccessControlSpec{
			TrustDomain: "public",
			AppPolicies: []config.AppPolicySpec{
				{
					AppName:       app1,
					DefaultAction: config.AllowAccess,
					TrustDomain:   "domain1",
					Namespace:     "ns1",
					AppOperationActions: []config.AppOperation{
						{
							Action:    config.AllowAccess,
							HTTPVerb:  []string{"POST", "GET"},
							Operation: "/op1",
						},
						{
							Action:    config.DenyAccess,
							HTTPVerb:  []string{"*"},
							Operation: "/op2",
						},
					},
				},
			},
		}

		accessControlList, _ := ParseAccessControlSpec(invalidAccessControlSpec, true)
		assert.Equal(t, config.DenyAccess, accessControlList.DefaultAction)
	})
}

func Test_isOperationAllowedByAccessControlPolicy(t *testing.T) {
	td := spiffeid.RequireTrustDomainFromString("public")
	privateTD := spiffeid.RequireTrustDomainFromString("private")
	t.Run("test when no acl specified", func(t *testing.T) {
		srcAppID := app1
		spiffeID, err := spiffe.FromStrings(td, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op1", common.HTTPExtension_POST, true, nil)
		// Action = Allow the operation since no ACL is defined
		assert.True(t, isAllowed)
	})

	t.Run("test when no matching app in acl found", func(t *testing.T) {
		srcAppID := "appX"
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(td, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op1", common.HTTPExtension_POST, true, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(privateTD, "ns1", srcAppID)
		require.NoError(t, err)

		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op1", common.HTTPExtension_POST, true, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when namespace does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(td, "abcd", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op1", common.HTTPExtension_POST, true, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when spiffe id is nil", func(t *testing.T) {
		accessControlList, _ := initializeAccessControlList(true)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(nil, "op1", common.HTTPExtension_POST, true, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when src app id is empty", func(t *testing.T) {
		accessControlList, _ := initializeAccessControlList(true)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(nil, "op1", common.HTTPExtension_POST, true, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when operation is not found in the policy spec", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(td, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "opX", common.HTTPExtension_POST, true, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.True(t, isAllowed)
	})

	t.Run("test http case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(td, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "Op2", common.HTTPExtension_POST, true, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb is not found", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)

		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op4", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Default action for the specific app
		assert.False(t, isAllowed)
	})

	t.Run("test when default action for app is not specified and no matching http verb found", func(t *testing.T) {
		srcAppID := app3
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op5", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Global Default action
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches *", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(true)
		spiffeID, err := spiffe.FromStrings(td, "ns1", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op2", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches a specific verb", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op4", common.HTTPExtension_POST, true, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when operation is invoked with /", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/op4", common.HTTPExtension_POST, true, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when http verb is not specified", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op4", common.HTTPExtension_NONE, true, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/op3/a", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test grpc case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/OP4", common.HTTPExtension_NONE, false, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/op3/b/b", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(true)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/op3/a/b", common.HTTPExtension_PUT, true, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})

	t.Run("test with grpc invocation", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op4", common.HTTPExtension_NONE, false, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})

	t.Run("when testing grpc calls, acl is not configured with http verb", func(t *testing.T) {
		srcAppID := app4
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op6", common.HTTPExtension_NONE, false, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})

	t.Run("when testing grpc calls, acl configured with wildcard * for full matching", func(t *testing.T) {
		srcAppID := app4
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op7/a/b/c", common.HTTPExtension_NONE, false, accessControlList)
		assert.True(t, isAllowed)

		isAllowed, _ = isOperationAllowedByAccessControlPolicy(spiffeID, "op7/a/b/c/d", common.HTTPExtension_NONE, false, accessControlList)
		assert.False(t, isAllowed)

		isAllowed, _ = isOperationAllowedByAccessControlPolicy(spiffeID, "op7/a/b/c/f", common.HTTPExtension_NONE, false, accessControlList)
		assert.True(t, isAllowed)
	})

	t.Run("when testing grpc calls, acl is configured with wildcards", func(t *testing.T) {
		srcAppID := app4
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op7/a/bc", common.HTTPExtension_NONE, false, accessControlList)
		assert.True(t, isAllowed)
	})

	t.Run("when testing grpc calls, acl configured with wildcard ** for full matching", func(t *testing.T) {
		srcAppID := app4
		accessControlList, _ := initializeAccessControlList(false)
		domain1TD := spiffeid.RequireTrustDomainFromString("domain1")
		spiffeID, err := spiffe.FromStrings(domain1TD, "ns2", srcAppID)
		require.NoError(t, err)
		isAllowed, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "op7/c/d/e", common.HTTPExtension_NONE, false, accessControlList)
		assert.True(t, isAllowed)
	})
}

func Test_isOperationAllowedByAccessControlPolicyEncoded(t *testing.T) {
	td := spiffeid.RequireTrustDomainFromString("public")
	spiffeID, err := spiffe.FromStrings(td, "ns1", app1)
	require.NoError(t, err)

	rule := func(action, op string, verbs ...string) config.AppOperation {
		return config.AppOperation{Action: action, HTTPVerb: verbs, Operation: op}
	}
	parse := func(t *testing.T, isHTTP bool, defaultAction string, ops ...config.AppOperation) *config.AccessControlList {
		t.Helper()
		acl, err := ParseAccessControlSpec(&config.AccessControlSpec{
			DefaultAction: defaultAction,
			TrustDomain:   "public",
			AppPolicies: []config.AppPolicySpec{{
				AppName:             app1,
				DefaultAction:       defaultAction,
				TrustDomain:         "public",
				Namespace:           "ns1",
				AppOperationActions: ops,
			}},
		}, isHTTP)
		require.NoError(t, err)
		return acl
	}
	denyList := []config.AppOperation{
		rule(config.DenyAccess, "/admin", "*"),
		rule(config.DenyAccess, "/admin/**", "*"),
		rule(config.DenyAccess, "/foo%20bar", "*"),
	}

	t.Run("http matches the decoded path without parameters", func(t *testing.T) {
		acl := parse(t, true, config.AllowAccess, denyList...)
		for op, allowed := range map[string]bool{
			"/public":         true,
			"/public;x":       true,
			"/files/%2A":      true,
			"/admin%zz":       false,
			"/public%zz":      false,
			"/%61dmin":        false,
			"/%41dmin":        false,
			"/admin;x":        false,
			"/admin%3Bx":      false,
			"/%61dmin/secret": false,
			"/admin;x/secret": false,
			"foo%20bar":       false,
			"foo bar":         false,
			// Dropping the parameters leaves a dot or empty segment.
			"/public/..;x/admin": false,
			"/public/.;x/admin":  false,
			"/;x/admin":          false,
		} {
			got, _ := isOperationAllowedByAccessControlPolicy(spiffeID, op, common.HTTPExtension_GET, true, acl)
			assert.Equalf(t, allowed, got, "operation %q", op)
		}
	})

	t.Run("http allows only what is allowed with and without parameters", func(t *testing.T) {
		acl := parse(t, true, config.DenyAccess,
			rule(config.AllowAccess, "/public", "*"),
			rule(config.AllowAccess, "/docs/**", "*"),
			rule(config.AllowAccess, "/files/%2A", "*"),
		)
		for op, allowed := range map[string]bool{
			"/public":       true,
			"/%70ublic":     true,
			"/public;admin": false,
			"/docs/a;v=1":   true,
			"/files/%2A":    true,
			"/files/%2a":    true,
			"/files/*":      false,
			"/files/secret": false,
		} {
			got, _ := isOperationAllowedByAccessControlPolicy(spiffeID, op, common.HTTPExtension_GET, true, acl)
			assert.Equalf(t, allowed, got, "operation %q", op)
		}
	})

	t.Run("http merges rules that resolve to the same operation, deny wins", func(t *testing.T) {
		acl := parse(t, true, config.AllowAccess,
			rule(config.DenyAccess, "/admin", "GET"),
			rule(config.DenyAccess, "/%61dmin", "POST"),
			rule(config.AllowAccess, "/%41DMIN", "GET", "DELETE"),
		)
		for verb, allowed := range map[common.HTTPExtension_Verb]bool{
			common.HTTPExtension_GET:    false,
			common.HTTPExtension_POST:   false,
			common.HTTPExtension_DELETE: true,
			common.HTTPExtension_PUT:    true,
		} {
			got, _ := isOperationAllowedByAccessControlPolicy(spiffeID, "/admin", verb, true, acl)
			assert.Equalf(t, allowed, got, "verb %s", verb)
		}
	})

	t.Run("grpc matches the operation literally", func(t *testing.T) {
		acl := parse(t, false, config.AllowAccess, denyList...)
		for op, allowed := range map[string]bool{
			"/admin":    false,
			"/%61dmin":  true,
			"/admin;x":  true,
			"foo%20bar": false,
			"foo bar":   true,
		} {
			got, _ := isOperationAllowedByAccessControlPolicy(spiffeID, op, common.HTTPExtension_NONE, false, acl)
			assert.Equalf(t, allowed, got, "operation %q", op)
		}
	})
}

// normalizeOperation was removed — the ACL no longer normalizes at
// runtime. Normalization is the responsibility of the service invocation
// entry points (NormalizeMethod). The behavior previously tested here is
// covered by pkg/method.TestNormalizeMethod.
