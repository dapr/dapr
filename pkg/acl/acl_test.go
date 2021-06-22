// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package acl

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/proto/common/v1"
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

func initializeAccessControlList(protocol string) (*config.AccessControlList, error) {
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
	accessControlList, err := ParseAccessControlSpec(inputSpec, protocol)

	return accessControlList, err
}

func TestParseAccessControlSpec(t *testing.T) {
	t.Run("translate to in-memory rules", func(t *testing.T) {
		accessControlList, err := initializeAccessControlList(config.HTTPProtocol)

		assert.Nil(t, err)

		assert.Equal(t, config.DenyAccess, accessControlList.DefaultAction)
		assert.Equal(t, "abcd", accessControlList.TrustDomain)

		// App1
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns1].AppName)
		assert.Equal(t, config.AllowAccess, accessControlList.PolicySpec[app1Ns1].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app1Ns1].Namespace)

		op1Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op1Actions.VerbAction["POST"] = config.AllowAccess
		op1Actions.VerbAction["GET"] = config.AllowAccess
		op1Actions.OperationAction = config.AllowAccess

		op2Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op2Actions.VerbAction["*"] = config.DenyAccess
		op2Actions.OperationAction = config.DenyAccess

		assert.Equal(t, 2, len(accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op1"].VerbAction))
		assert.Equal(t, op1Actions, accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op1"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op2"].VerbAction))
		assert.Equal(t, op2Actions, accessControlList.PolicySpec[app1Ns1].AppOperationActions["/op2"])

		// App2
		assert.Equal(t, app2, accessControlList.PolicySpec[app2Ns2].AppName)
		assert.Equal(t, config.DenyAccess, accessControlList.PolicySpec[app2Ns2].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app2Ns2].TrustDomain)
		assert.Equal(t, "ns2", accessControlList.PolicySpec[app2Ns2].Namespace)

		op3Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/a/*",
			VerbAction:       make(map[string]string),
		}
		op3Actions.VerbAction["PUT"] = config.AllowAccess
		op3Actions.VerbAction["GET"] = config.AllowAccess
		op3Actions.OperationAction = config.AllowAccess

		op4Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op4Actions.VerbAction["POST"] = config.AllowAccess
		op4Actions.OperationAction = config.AllowAccess

		assert.Equal(t, 2, len(accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op3"].VerbAction))
		assert.Equal(t, op3Actions, accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op3"])
		assert.Equal(t, 1, len(accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op4"].VerbAction))
		assert.Equal(t, op4Actions, accessControlList.PolicySpec[app2Ns2].AppOperationActions["/op4"])

		// App3
		assert.Equal(t, app3, accessControlList.PolicySpec[app3Ns1].AppName)
		assert.Equal(t, "", accessControlList.PolicySpec[app3Ns1].DefaultAction)
		assert.Equal(t, "domain1", accessControlList.PolicySpec[app3Ns1].TrustDomain)
		assert.Equal(t, "ns1", accessControlList.PolicySpec[app3Ns1].Namespace)

		op5Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op5Actions.VerbAction["POST"] = config.AllowAccess
		op5Actions.OperationAction = config.AllowAccess

		assert.Equal(t, 1, len(accessControlList.PolicySpec[app3Ns1].AppOperationActions["/op5"].VerbAction))
		assert.Equal(t, op5Actions, accessControlList.PolicySpec[app3Ns1].AppOperationActions["/op5"])

		// App1 with a different namespace
		assert.Equal(t, app1, accessControlList.PolicySpec[app1Ns4].AppName)
		assert.Equal(t, config.AllowAccess, accessControlList.PolicySpec[app1Ns4].DefaultAction)
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns4].TrustDomain)
		assert.Equal(t, "ns4", accessControlList.PolicySpec[app1Ns4].Namespace)

		op6Actions := config.AccessControlListOperationAction{
			OperationPostFix: "/",
			VerbAction:       make(map[string]string),
		}
		op6Actions.VerbAction["*"] = config.AllowAccess
		op6Actions.OperationAction = config.AllowAccess

		assert.Equal(t, 1, len(accessControlList.PolicySpec[app1Ns4].AppOperationActions["/op6"].VerbAction))
		assert.Equal(t, op6Actions, accessControlList.PolicySpec[app1Ns4].AppOperationActions["/op6"])
	})

	t.Run("test when no trust domain and namespace specified in app policy", func(t *testing.T) {
		invalidAccessControlSpec := config.AccessControlSpec{
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

		_, err := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Error(t, err, "invalid access control spec. missing trustdomain for apps: [%s], missing namespace for apps: [%s], missing app name on at least one of the app policies: true", app1, app2)
	})

	t.Run("test when no trust domain is specified for the app", func(t *testing.T) {
		accessControlSpec := config.AccessControlSpec{
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

		accessControlList, _ := ParseAccessControlSpec(accessControlSpec, "http")
		assert.Equal(t, "public", accessControlList.PolicySpec[app1Ns1].TrustDomain)
	})

	t.Run("test when no access control policy has been specified", func(t *testing.T) {
		invalidAccessControlSpec := config.AccessControlSpec{
			DefaultAction: "",
			TrustDomain:   "",
			AppPolicies:   []config.AppPolicySpec{},
		}

		accessControlList, _ := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Nil(t, accessControlList)
	})

	t.Run("test when no default global action has been specified", func(t *testing.T) {
		invalidAccessControlSpec := config.AccessControlSpec{
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

		accessControlList, _ := ParseAccessControlSpec(invalidAccessControlSpec, "http")
		assert.Equal(t, accessControlList.DefaultAction, config.DenyAccess)
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
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, nil)
		// Action = Allow the operation since no ACL is defined
		assert.True(t, isAllowed)
	})

	t.Run("test when no matching app in acl found", func(t *testing.T) {
		srcAppID := "appX"
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when trust domain does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "private",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when namespace does not match", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "abcd",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply global default action
		assert.False(t, isAllowed)
	})

	t.Run("test when spiffe id is nil", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when src app id is empty", func(t *testing.T) {
		srcAppID := ""
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(nil, srcAppID, "op1", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Default global action
		assert.False(t, isAllowed)
	})

	t.Run("test when operation is not found in the policy spec", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "opX", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.True(t, isAllowed)
	})

	t.Run("test http case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "Op2", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Ignore policy and apply default action for app
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb is not found", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Default action for the specific app
		assert.False(t, isAllowed)
	})

	t.Run("test when default action for app is not specified and no matching http verb found", func(t *testing.T) {
		srcAppID := app3
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op5", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Global Default action
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches *", func(t *testing.T) {
		srcAppID := app1
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "public",
			Namespace:   "ns1",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op2", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when http verb matches a specific verb", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when operation is invoked with /", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op4", common.HTTPExtension_POST, config.HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test when http verb is not specified", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_NONE, config.HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.True(t, isAllowed)
	})

	t.Run("test grpc case-sensitivity when matching operation post fix", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.GRPCProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/OP4", common.HTTPExtension_NONE, config.GRPCProtocol, accessControlList)
		// Action = Default action for the specific verb
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/b/b", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.False(t, isAllowed)
	})

	t.Run("test when non-matching operation post fix is specified in policy spec", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.HTTPProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "/op3/a/b", common.HTTPExtension_PUT, config.HTTPProtocol, accessControlList)
		// Action = Default action for the app
		assert.True(t, isAllowed)
	})

	t.Run("test with grpc invocation", func(t *testing.T) {
		srcAppID := app2
		accessControlList, _ := initializeAccessControlList(config.GRPCProtocol)
		spiffeID := config.SpiffeID{
			TrustDomain: "domain1",
			Namespace:   "ns2",
			AppID:       srcAppID,
		}
		isAllowed, _ := IsOperationAllowedByAccessControlPolicy(&spiffeID, srcAppID, "op4", common.HTTPExtension_NONE, config.GRPCProtocol, accessControlList)
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

func TestNormalizeOperation(t *testing.T) {
	t.Run("normal path no slash", func(t *testing.T) {
		p := "path"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "path", p)
	})

	t.Run("normal path caps", func(t *testing.T) {
		p := "Path"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "Path", p)
	})

	t.Run("single slash", func(t *testing.T) {
		p := "/path"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "/path", p)
	})

	t.Run("multiple slashes", func(t *testing.T) {
		p := "///path"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "/path", p)
	})

	t.Run("prefix", func(t *testing.T) {
		p := "../path"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "path", p)
	})

	t.Run("encoded", func(t *testing.T) {
		p := "path%72"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "pathr", p)
	})

	t.Run("normal multiple paths", func(t *testing.T) {
		p := "path1/path2/path3"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "path1/path2/path3", p)
	})

	t.Run("normal multiple paths leading slash", func(t *testing.T) {
		p := "/path1/path2/path3"
		p, _ = normalizeOperation(p)

		assert.Equal(t, "/path1/path2/path3", p)
	})
}
