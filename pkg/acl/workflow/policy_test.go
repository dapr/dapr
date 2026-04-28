/*
Copyright 2026 The Dapr Authors
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

package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/kit/ptr"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

func makePolicy(name string, rules []wfaclapi.WorkflowAccessPolicyRule) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			Rules: rules,
		},
	}
}

func TestCompile_NilWhenNoPolicies(t *testing.T) {
	cp := Compile(nil, "")
	assert.Nil(t, cp)

	cp = Compile([]wfaclapi.WorkflowAccessPolicy{}, "")
	assert.Nil(t, cp)
}

func TestEvaluate_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.Evaluate("", "any-app", OperationTypeWorkflow, "AnyWorkflow"))
}

func TestEvaluate_DefaultDenyWhenPoliciesExist(t *testing.T) {
	// Policy with no rules — defaults to deny all.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("deny-all", nil),
	}, "")

	// Even though the policy has no rules, a non-nil CompiledPolicies
	// means policies exist, so the default is deny.
	assert.False(t, cp.Evaluate("", "any-app", OperationTypeWorkflow, "AnyWorkflow"))
}

func TestEvaluate_AllowSpecificCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "checkout"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ProcessOrder",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "checkout", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("", "other-app", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("", "checkout", OperationTypeWorkflow, "OtherWorkflow"))
	assert.False(t, cp.Evaluate("", "checkout", OperationTypeActivity, "ProcessOrder"))
}

func TestEvaluate_GlobPatterns(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Process*",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeActivity,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessRefund"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "CancelOrder"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeActivity, "AnyActivity"))
}

func TestEvaluate_MostSpecificWins(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionDeny,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Process*",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ProcessSecret",
						Action: wfaclapi.PolicyActionDeny,
					},
				},
			},
		}),
	}, "")

	// "Process*" (prefix len 7) is more specific than "*" (prefix len 0)
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
	// Exact match "ProcessSecret" is more specific than glob "Process*"
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessSecret"))
	// Wildcard "*" matches but action is deny
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "CancelOrder"))
}

func TestEvaluate_DenyWinsTies(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Order*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "Order*",
						Action: wfaclapi.PolicyActionDeny,
					},
				},
			},
		}),
	}, "")

	// Same specificity, deny wins.
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "OrderProcess"))
}

func TestEvaluate_MultipleCallers(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{
					{AppID: "app-a"},
					{AppID: "app-b"},
				},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "Any"))
	assert.True(t, cp.Evaluate("", "app-b", OperationTypeWorkflow, "Any"))
	assert.False(t, cp.Evaluate("", "app-c", OperationTypeWorkflow, "Any"))
}

func TestEvaluate_MultiplePoliciesMerged(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("policy1", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "WorkflowA",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
		makePolicy("policy2", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-b"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "WorkflowB",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WorkflowA"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WorkflowB"))
	assert.True(t, cp.Evaluate("", "app-b", OperationTypeWorkflow, "WorkflowB"))
	assert.False(t, cp.Evaluate("", "app-b", OperationTypeWorkflow, "WorkflowA"))
}

func TestEvaluate_InvalidGlobSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "[invalid",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "ValidWorkflow",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	// The invalid glob should be skipped, the valid one should still work.
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ValidWorkflow"))
}

func TestEvaluate_SelfInvocation(t *testing.T) {
	// An app calling its own workflows must have itself in the callers list.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "my-app"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type:   wfaclapi.WorkflowOperationTypeWorkflow,
						Name:   "*",
						Action: wfaclapi.PolicyActionAllow,
					},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "my-app", OperationTypeWorkflow, "SelfWorkflow"))
	assert.False(t, cp.Evaluate("", "other-app", OperationTypeWorkflow, "SelfWorkflow"))
}

func TestLiteralPrefixLen(t *testing.T) {
	assert.Equal(t, 0, literalPrefixLen("*"))
	assert.Equal(t, 7, literalPrefixLen("Process*"))
	assert.Equal(t, 12, literalPrefixLen("ProcessOrder"))
	assert.Equal(t, 0, literalPrefixLen("?anything"))
	assert.Equal(t, 3, literalPrefixLen("abc[def]"))
}

func TestContainsWildcard(t *testing.T) {
	assert.True(t, containsWildcard("*"))
	assert.True(t, containsWildcard("Process*"))
	assert.True(t, containsWildcard("test?"))
	assert.True(t, containsWildcard("[abc]"))
	assert.False(t, containsWildcard("ExactMatch"))
	assert.False(t, containsWildcard(""))
}

// --- Edge case tests ---

func TestEvaluate_DefaultActionAllow(t *testing.T) {
	// When DefaultAction is "allow", unmatched operations are allowed.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionAllow,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type:   wfaclapi.WorkflowOperationTypeWorkflow,
					Name:   "SpecificWF",
					Action: wfaclapi.PolicyActionDeny,
				}},
			}},
		},
	}}, "")

	// SpecificWF is explicitly denied.
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "SpecificWF"))
	// OtherWF has no matching rule — DefaultAction "allow" kicks in.
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "OtherWF"))
}

func TestEvaluate_DefaultActionDeny(t *testing.T) {
	// When DefaultAction is "deny" (or empty), unmatched operations are denied.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type:   wfaclapi.WorkflowOperationTypeWorkflow,
					Name:   "AllowedWF",
					Action: wfaclapi.PolicyActionAllow,
				}},
			}},
		},
	}}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "AllowedWF"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "OtherWF"))
}

func TestEvaluate_DefaultActionEmpty(t *testing.T) {
	// Empty DefaultAction defaults to "deny".
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: "", // empty = deny
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type:   wfaclapi.WorkflowOperationTypeWorkflow,
					Name:   "AllowedWF",
					Action: wfaclapi.PolicyActionAllow,
				}},
			}},
		},
	}}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "AllowedWF"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "OtherWF"))
}

func TestEvaluate_MultiplePoliciesDenyWinsDefault(t *testing.T) {
	// If ANY policy has DefaultAction deny, the aggregate is deny.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		{Spec: wfaclapi.WorkflowAccessPolicySpec{DefaultAction: wfaclapi.PolicyActionAllow}},
		{Spec: wfaclapi.WorkflowAccessPolicySpec{DefaultAction: wfaclapi.PolicyActionDeny}},
	}, "")

	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "AnyWF"))
}

func TestEvaluate_EmptyCallersSkipped(t *testing.T) {
	// Rules with empty callers are skipped as defense-in-depth.
	// The CRD validates MinItems=1, but standalone mode could bypass validation.
	// An empty callers list would otherwise match ALL callers.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{}, // empty
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type:   wfaclapi.WorkflowOperationTypeWorkflow,
				Name:   "*",
				Action: wfaclapi.PolicyActionAllow,
			}},
		}}),
	}, "")

	// Empty callers rule is skipped → no matching rule → default deny.
	assert.False(t, cp.Evaluate("", "any-app", OperationTypeWorkflow, "AnyWF"))
}

func TestEvaluate_CrossPolicyConflictingActions(t *testing.T) {
	// Two policies with identical patterns but opposite actions.
	// Deny should win regardless of the order policies are provided.
	allowPolicy := makePolicy("allow-policy", []wfaclapi.WorkflowAccessPolicyRule{{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
		Operations: []wfaclapi.WorkflowOperationRule{{
			Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process*", Action: wfaclapi.PolicyActionAllow,
		}},
	}})
	denyPolicy := makePolicy("deny-policy", []wfaclapi.WorkflowAccessPolicyRule{{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
		Operations: []wfaclapi.WorkflowOperationRule{{
			Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process*", Action: wfaclapi.PolicyActionDeny,
		}},
	}})

	// Allow first, deny second.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{allowPolicy, denyPolicy}, "")
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))

	// Deny first, allow second — still deny wins.
	cp = Compile([]wfaclapi.WorkflowAccessPolicy{denyPolicy, allowPolicy}, "")
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
}

func TestEvaluate_QuestionMarkGlob(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process?", Action: wfaclapi.PolicyActionAllow,
			}},
		}}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessA"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessZ"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessAB"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "Process"))
}

func TestEvaluate_CharacterClassGlob(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "[A-Z]*", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "Process[ABC]", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "processOrder"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeActivity, "ProcessA"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeActivity, "ProcessC"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeActivity, "ProcessD"))
}

func TestEvaluate_CaseSensitive(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "ProcessOrder", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "processorder"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "PROCESSORDER"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessAnything"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "processanything"))
}

func TestEvaluate_TypeIsolation(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// Activity rule should NOT match workflow queries.
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "AnyWorkflow"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeActivity, "AnyActivity"))

	// Reverse: workflow-only rule.
	cp2 := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp2.Evaluate("", "app-a", OperationTypeWorkflow, "AnyWorkflow"))
	assert.False(t, cp2.Evaluate("", "app-a", OperationTypeActivity, "AnyActivity"))
}

func TestEvaluate_MultipleRulesForSameCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF1", Action: wfaclapi.PolicyActionAllow},
				},
			},
			{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF2", Action: wfaclapi.PolicyActionAllow},
				},
			},
		}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF1"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF2"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF3"))
}

func TestEvaluate_ExactMatchBeatsGlobAtSamePrefix(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "ProcessOrder*", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "ProcessOrder", Action: wfaclapi.PolicyActionDeny},
			},
		}}),
	}, "")

	// Exact match "ProcessOrder" (deny) beats glob "ProcessOrder*" (allow)
	// because isExact=true wins over isExact=false.
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrder"))
	// But "ProcessOrderX" only matches the glob, so it's allowed.
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "ProcessOrderX"))
}

func TestEvaluate_EmptyOperationName(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// Empty operation name should still match "*".
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, ""))
}

func TestEvaluate_SpecialCharactersInName(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "my.workflow", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "my-workflow", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "my_workflow", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "my.workflow"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "my-workflow"))
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "my_workflow"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "myXworkflow"))
}

func TestEvaluate_ManyPoliciesStress(t *testing.T) {
	policies := make([]wfaclapi.WorkflowAccessPolicy, 0, 51)
	for range 50 {
		ops := make([]wfaclapi.WorkflowOperationRule, 0, 10)
		for range 10 {
			ops = append(ops, wfaclapi.WorkflowOperationRule{
				Type:   wfaclapi.WorkflowOperationTypeWorkflow,
				Name:   "WF_*",
				Action: wfaclapi.PolicyActionDeny,
			})
		}
		policies = append(policies, makePolicy("policy", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers:    []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: ops,
		}}))
	}

	// Add one policy that allows a specific workflow.
	policies = append(policies, makePolicy("allow", []wfaclapi.WorkflowAccessPolicyRule{{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
		Operations: []wfaclapi.WorkflowOperationRule{{
			Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF_SpecificOne", Action: wfaclapi.PolicyActionAllow,
		}},
	}}))

	cp := Compile(policies, "")
	// Exact match "WF_SpecificOne" (allow) beats glob "WF_*" (deny).
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF_SpecificOne"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF_Other"))
}

func TestCompile_AllRulesInvalidGlobSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "[invalid", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "[also-invalid", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// All operations invalid → rule has 0 compiled ops → not added.
	// But CompiledPolicies is still non-nil (policies exist), so default deny.
	assert.NotNil(t, cp)
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "anything"))
}

func TestCompile_EmptyRulesInPolicy(t *testing.T) {
	// Policy with nil rules — compiles to non-nil CompiledPolicies
	// (policies exist = default deny), but with 0 compiled rules.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("empty", nil),
	}, "")

	assert.NotNil(t, cp)
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "anything"))
}

func TestEvaluate_CallerNotInAnyRule(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// app-b not in any rule → no match → default deny.
	assert.False(t, cp.Evaluate("", "app-b", OperationTypeWorkflow, "AnyWF"))
}

func TestEvaluate_BroadCallerWithDenyOverride(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{
			{
				// Both app-a and app-b are allowed for all workflows.
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}, {AppID: "app-b"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
				},
			},
			{
				// Specific deny for app-a on SecretWF.
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "SecretWF", Action: wfaclapi.PolicyActionDeny},
				},
			},
		}),
	}, "")

	// app-a: "SecretWF" — both rules match, but "SecretWF" (exact, deny) is more specific than "*" (glob, allow).
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "SecretWF"))
	// app-a: other workflows — only broad rule matches → allow.
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "PublicWF"))
	// app-b: SecretWF — only broad rule matches (app-b not in deny rule) → allow.
	assert.True(t, cp.Evaluate("", "app-b", OperationTypeWorkflow, "SecretWF"))
	// app-c: not in any rule → default deny.
	assert.False(t, cp.Evaluate("", "app-c", OperationTypeWorkflow, "PublicWF"))
}

// --- Standalone validation edge cases ---
// These test how the engine handles invalid input that would be rejected by
// Kubernetes but silently passes through in standalone mode (yaml.Unmarshal).

func TestCompile_Standalone_InvalidActionSilentlyFails(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF", Action: wfaclapi.PolicyAction("invalid")},
			},
		}}),
	}, "")

	// "invalid" action is not "allow", so Evaluate returns false (effective deny).
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF"))
}

func TestCompile_Standalone_InvalidTypeSilentlyFails(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationType("bogus"), Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// "bogus" type never matches workflow or activity queries → dead rule.
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeActivity, "Act"))
}

func TestCompile_Standalone_EmptyNamePattern(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// Empty pattern only matches empty operation name.
	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, ""))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "AnyWF"))
}

// --- IsCallerKnown tests ---

func TestIsCallerKnown_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.IsCallerKnown("", "any-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_CallerWithSpecificAllowOnly(t *testing.T) {
	// A caller with only a specific-name allow rule (no wildcard) is NOT
	// known: non-subject methods could target an instance whose name is
	// outside the caller's allow list, so unconditional trust isn't warranted.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF1", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.False(t, cp.IsCallerKnown("", "app-a", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("", "app-b", OperationTypeWorkflow))
}

func TestIsCallerKnown_CallerWithWildcardAllow(t *testing.T) {
	// A caller with a wildcard allow and no deny rules is known.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "trusted"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp.IsCallerKnown("", "trusted", OperationTypeWorkflow))
	// Wildcard allow for workflow does not imply known for activity.
	assert.False(t, cp.IsCallerKnown("", "trusted", OperationTypeActivity))
}

func TestIsCallerKnown_CallerOnlyInDenyRule(t *testing.T) {
	// A caller that only appears in deny rules is NOT known.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "deny-only-app"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionDeny},
			},
		}}),
	}, "")

	assert.False(t, cp.IsCallerKnown("", "deny-only-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_CallerWithMixedAllowAndDeny(t *testing.T) {
	// A caller with both allow and deny rules is NOT known: the deny rule
	// signals conditional trust, and non-subject methods could target an
	// instance the caller is specifically denied from. Regression test for
	// the partial-auth bypass cicoyle flagged in #9790.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "mixed-app"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "AllowedWF", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "DeniedWF", Action: wfaclapi.PolicyActionDeny},
			},
		}}),
	}, "")

	assert.False(t, cp.IsCallerKnown("", "mixed-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_WildcardAllowWithSpecificDeny(t *testing.T) {
	// A caller with a wildcard allow PLUS a specific deny is also NOT known:
	// the deny rule means the caller cannot be trusted to invoke non-subject
	// methods that may target the denied instance.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "SecretWF", Action: wfaclapi.PolicyActionDeny},
			},
		}}),
	}, "")

	assert.False(t, cp.IsCallerKnown("", "app-a", OperationTypeWorkflow))
}

func TestIsCallerKnown_MultiplePolicies(t *testing.T) {
	// Wildcard allow in one policy, denied in another, and unmentioned cases.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("deny-policy", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "deny-app"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionDeny},
			},
		}}),
		makePolicy("allow-policy", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "allow-app"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	assert.True(t, cp.IsCallerKnown("", "allow-app", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("", "deny-app", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("", "unknown-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_EmptyPolicyNoRules(t *testing.T) {
	// Non-nil compiled policies but no rules.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("empty", nil),
	}, "")

	assert.False(t, cp.IsCallerKnown("", "any-app", OperationTypeWorkflow))
}

func TestCompile_Standalone_EmptyAppIDInCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: ""}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "")

	// Empty AppID in callers map — only matches callers with empty ID.
	assert.True(t, cp.Evaluate("", "", OperationTypeWorkflow, "WF"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "WF"))
}

func TestCompile_Standalone_NoDefaultAction(t *testing.T) {
	// DefaultAction is "" (omitted) — treated as "deny".
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: "", // omitted in standalone YAML
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Allowed", Action: wfaclapi.PolicyActionAllow},
				},
			}},
		},
	}}, "")

	assert.True(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "Allowed"))
	assert.False(t, cp.Evaluate("", "app-a", OperationTypeWorkflow, "Other"))
}

// --- Cross-namespace caller tests ---

// Same-namespace caller matches a rule with nil Namespace (legacy behavior).
func TestEvaluate_SameNsCallerWithNamespacelessRule(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("p", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "caller"}}, // Namespace: nil
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "target-ns")

	assert.True(t, cp.Evaluate("target-ns", "caller", OperationTypeWorkflow, "WF"))
	assert.False(t, cp.Evaluate("other-ns", "caller", OperationTypeWorkflow, "WF"), "cross-ns caller must not match a nil-ns rule")
}

// With the feature gate enabled, an explicit cross-ns rule matches a cross-ns caller.
func TestEvaluate_CrossNsCallerAllowedWhenFeatureEnabled(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("p", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "caller", Namespace: ptr.Of("other-ns")}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "target-ns")

	assert.True(t, cp.Evaluate("other-ns", "caller", OperationTypeWorkflow, "WF"))
	assert.False(t, cp.Evaluate("target-ns", "caller", OperationTypeWorkflow, "WF"), "same-ns caller must not match a cross-ns rule")
	assert.False(t, cp.Evaluate("yet-another-ns", "caller", OperationTypeWorkflow, "WF"), "different cross-ns must not match")
}

// IsCallerKnown keys on (ns, appID) — a same-appID caller in a different
// namespace is NOT known unless a rule allows them.
func TestIsCallerKnown_NamespaceAware(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("p", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{
				{AppID: "caller"}, // same-ns
				{AppID: "friend", Namespace: ptr.Of("other-ns")}, // cross-ns
			},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	}, "target-ns")

	assert.True(t, cp.IsCallerKnown("target-ns", "caller", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("other-ns", "caller", OperationTypeWorkflow), "same AppID in a different ns is a different caller")
	assert.True(t, cp.IsCallerKnown("other-ns", "friend", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("target-ns", "friend", OperationTypeWorkflow), "cross-ns rule must not leak into same-ns")
}
