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
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/kit/ptr"
)

// evalAllowed runs Evaluate and returns just the allow/deny decision- used by tests
// that don't care about the DenialReason.
func evalAllowed(cp *CompiledPolicies, callerAppID string, opType OperationType, opName string, history *protos.PropagatedHistory) bool {
	allowed, _ := cp.Evaluate(callerAppID, opType, opName, history)
	return allowed
}

func makePolicy(name string, rules []wfaclapi.WorkflowAccessPolicyRule) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			Rules: rules,
		},
	}
}

func TestCompile_NilWhenNoPolicies(t *testing.T) {
	cp := Compile(nil)
	assert.Nil(t, cp)

	cp = Compile([]wfaclapi.WorkflowAccessPolicy{})
	assert.Nil(t, cp)
}

func TestEvaluate_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, evalAllowed(cp, "any-app", OperationTypeWorkflow, "AnyWorkflow", nil))
}

func TestEvaluate_DefaultDenyWhenPoliciesExist(t *testing.T) {
	// Policy with no rules — defaults to deny all.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("deny-all", nil),
	})

	// Even though the policy has no rules, a non-nil CompiledPolicies
	// means policies exist, so the default is deny.
	assert.False(t, evalAllowed(cp, "any-app", OperationTypeWorkflow, "AnyWorkflow", nil))
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
	})

	assert.True(t, evalAllowed(cp, "checkout", OperationTypeWorkflow, "ProcessOrder", nil))
	assert.False(t, evalAllowed(cp, "other-app", OperationTypeWorkflow, "ProcessOrder", nil))
	assert.False(t, evalAllowed(cp, "checkout", OperationTypeWorkflow, "OtherWorkflow", nil))
	assert.False(t, evalAllowed(cp, "checkout", OperationTypeActivity, "ProcessOrder", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessRefund", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "CancelOrder", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "AnyActivity", nil))
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
	})

	// "Process*" (prefix len 7) is more specific than "*" (prefix len 0)
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
	// Exact match "ProcessSecret" is more specific than glob "Process*"
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessSecret", nil))
	// Wildcard "*" matches but action is deny
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "CancelOrder", nil))
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
	})

	// Same specificity, deny wins.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "OrderProcess", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Any", nil))
	assert.True(t, evalAllowed(cp, "app-b", OperationTypeWorkflow, "Any", nil))
	assert.False(t, evalAllowed(cp, "app-c", OperationTypeWorkflow, "Any", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WorkflowA", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WorkflowB", nil))
	assert.True(t, evalAllowed(cp, "app-b", OperationTypeWorkflow, "WorkflowB", nil))
	assert.False(t, evalAllowed(cp, "app-b", OperationTypeWorkflow, "WorkflowA", nil))
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
	})

	// The invalid glob should be skipped, the valid one should still work.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ValidWorkflow", nil))
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
	})

	assert.True(t, evalAllowed(cp, "my-app", OperationTypeWorkflow, "SelfWorkflow", nil))
	assert.False(t, evalAllowed(cp, "other-app", OperationTypeWorkflow, "SelfWorkflow", nil))
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
	}})

	// SpecificWF is explicitly denied.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "SpecificWF", nil))
	// OtherWF has no matching rule — DefaultAction "allow" kicks in.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "OtherWF", nil))
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
	}})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "AllowedWF", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "OtherWF", nil))
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
	}})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "AllowedWF", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "OtherWF", nil))
}

func TestEvaluate_MultiplePoliciesDenyWinsDefault(t *testing.T) {
	// If ANY policy has DefaultAction deny, the aggregate is deny.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		{Spec: wfaclapi.WorkflowAccessPolicySpec{DefaultAction: wfaclapi.PolicyActionAllow}},
		{Spec: wfaclapi.WorkflowAccessPolicySpec{DefaultAction: wfaclapi.PolicyActionDeny}},
	})

	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "AnyWF", nil))
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
	})

	// Empty callers rule is skipped = no matching rule = default deny.
	assert.False(t, evalAllowed(cp, "any-app", OperationTypeWorkflow, "AnyWF", nil))
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
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{allowPolicy, denyPolicy})
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))

	// Deny first, allow second — still deny wins.
	cp = Compile([]wfaclapi.WorkflowAccessPolicy{denyPolicy, allowPolicy})
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
}

func TestEvaluate_QuestionMarkGlob(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "Process?", Action: wfaclapi.PolicyActionAllow,
			}},
		}}),
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessA", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessZ", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessAB", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Process", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "processOrder", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "ProcessA", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "ProcessC", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "ProcessD", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "processorder", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "PROCESSORDER", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessAnything", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "processanything", nil))
}

func TestEvaluate_TypeIsolation(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// Activity rule should NOT match workflow queries.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "AnyWorkflow", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "AnyActivity", nil))

	// Reverse: workflow-only rule.
	cp2 := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	assert.True(t, evalAllowed(cp2, "app-a", OperationTypeWorkflow, "AnyWorkflow", nil))
	assert.False(t, evalAllowed(cp2, "app-a", OperationTypeActivity, "AnyActivity", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF1", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF2", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF3", nil))
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
	})

	// Exact match "ProcessOrder" (deny) beats glob "ProcessOrder*" (allow)
	// because isExact=true wins over isExact=false.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrder", nil))
	// But "ProcessOrderX" only matches the glob, so it's allowed.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "ProcessOrderX", nil))
}

func TestEvaluate_EmptyOperationName(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// Empty operation name should still match "*".
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "", nil))
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
	})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "my.workflow", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "my-workflow", nil))
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "my_workflow", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "myXworkflow", nil))
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

	cp := Compile(policies)
	// Exact match "WF_SpecificOne" (allow) beats glob "WF_*" (deny).
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF_SpecificOne", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF_Other", nil))
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
	})

	// All operations invalid = rule has 0 compiled ops = not added.
	// But CompiledPolicies is still non-nil (policies exist), so default deny.
	assert.NotNil(t, cp)
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "anything", nil))
}

func TestCompile_EmptyRulesInPolicy(t *testing.T) {
	// Policy with nil rules — compiles to non-nil CompiledPolicies
	// (policies exist = default deny), but with 0 compiled rules.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("empty", nil),
	})

	assert.NotNil(t, cp)
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "anything", nil))
}

func TestEvaluate_CallerNotInAnyRule(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// app-b not in any rule = no match = default deny.
	assert.False(t, evalAllowed(cp, "app-b", OperationTypeWorkflow, "AnyWF", nil))
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
	})

	// app-a: "SecretWF" — both rules match, but "SecretWF" (exact, deny) is more specific than "*" (glob, allow).
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "SecretWF", nil))
	// app-a: other workflows — only broad rule matches = allow.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "PublicWF", nil))
	// app-b: SecretWF — only broad rule matches (app-b not in deny rule) = allow.
	assert.True(t, evalAllowed(cp, "app-b", OperationTypeWorkflow, "SecretWF", nil))
	// app-c: not in any rule = default deny.
	assert.False(t, evalAllowed(cp, "app-c", OperationTypeWorkflow, "PublicWF", nil))
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
	})

	// "invalid" action is not "allow", so Evaluate returns false (effective deny).
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF", nil))
}

func TestCompile_Standalone_InvalidTypeSilentlyFails(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationType("bogus"), Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// "bogus" type never matches workflow or activity queries = dead rule.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Act", nil))
}

func TestCompile_Standalone_EmptyNamePattern(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// Empty pattern only matches empty operation name.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "AnyWF", nil))
}

// --- IsCallerKnown tests ---

func TestIsCallerKnown_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.IsCallerKnown("any-app", OperationTypeWorkflow))
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
	})

	assert.False(t, cp.IsCallerKnown("app-a", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("app-b", OperationTypeWorkflow))
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
	})

	assert.True(t, cp.IsCallerKnown("trusted", OperationTypeWorkflow))
	// Wildcard allow for workflow does not imply known for activity.
	assert.False(t, cp.IsCallerKnown("trusted", OperationTypeActivity))
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
	})

	assert.False(t, cp.IsCallerKnown("deny-only-app", OperationTypeWorkflow))
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
	})

	assert.False(t, cp.IsCallerKnown("mixed-app", OperationTypeWorkflow))
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
	})

	assert.False(t, cp.IsCallerKnown("app-a", OperationTypeWorkflow))
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
	})

	assert.True(t, cp.IsCallerKnown("allow-app", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("deny-app", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("unknown-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_EmptyPolicyNoRules(t *testing.T) {
	// Non-nil compiled policies but no rules.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("empty", nil),
	})

	assert.False(t, cp.IsCallerKnown("any-app", OperationTypeWorkflow))
}

func TestCompile_Standalone_EmptyAppIDInCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: ""}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "*", Action: wfaclapi.PolicyActionAllow},
			},
		}}),
	})

	// Empty AppID in callers map — only matches callers with empty ID.
	assert.True(t, evalAllowed(cp, "", OperationTypeWorkflow, "WF", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF", nil))
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
	}})

	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Allowed", nil))
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Other", nil))
}

// --- Requires (history-gated rules) tests ---

type historyBuilder struct {
	events []*protos.HistoryEvent
	chunks []*protos.PropagatedHistoryChunk
}

func newHistory() *historyBuilder { return &historyBuilder{} }

func (h *historyBuilder) startChunk(appID string) *historyBuilder {
	h.chunks = append(h.chunks, &protos.PropagatedHistoryChunk{
		AppId:           appID,
		StartEventIndex: int32(len(h.events)),
	})
	return h
}

func (h *historyBuilder) addTaskScheduled(eventID int32, name string) *historyBuilder {
	h.events = append(h.events, &protos.HistoryEvent{
		EventId: eventID,
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: name},
		},
	})
	h.bumpChunk()
	return h
}

func (h *historyBuilder) addTaskCompleted(scheduledID int32) *historyBuilder {
	h.events = append(h.events, &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: scheduledID},
		},
	})
	h.bumpChunk()
	return h
}

func (h *historyBuilder) addTaskFailed(scheduledID int32) *historyBuilder {
	h.events = append(h.events, &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: scheduledID},
		},
	})
	h.bumpChunk()
	return h
}

func (h *historyBuilder) addEventRaised(name string) *historyBuilder {
	h.events = append(h.events, &protos.HistoryEvent{
		EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: name, Input: wrapperspb.String("")},
		},
	})
	h.bumpChunk()
	return h
}

func (h *historyBuilder) bumpChunk() {
	if len(h.chunks) == 0 {
		h.chunks = append(h.chunks, &protos.PropagatedHistoryChunk{StartEventIndex: 0})
	}
	h.chunks[len(h.chunks)-1].EventCount++
}

func (h *historyBuilder) build() *protos.PropagatedHistory {
	return &protos.PropagatedHistory{Events: h.events, Chunks: h.chunks}
}

func TestEvaluateWithHistory_RequiresActivityCompleted(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "order-service"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type:   wfaclapi.WorkflowOperationTypeActivity,
				Name:   "ProcessPayment",
				Action: wfaclapi.PolicyActionAllow,
				Requires: []wfaclapi.RequiredEvent{
					{EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "FraudCheckPassed"},
					{EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "HumanApprovalReceived"},
				},
			}},
		}}),
	})

	// History contains both required completions = allowed.
	full := newHistory().
		startChunk("order-service").
		addTaskScheduled(1, "FraudCheckPassed").
		addTaskCompleted(1).
		addTaskScheduled(2, "HumanApprovalReceived").
		addTaskCompleted(2).
		build()
	assert.True(t, evalAllowed(cp, "order-service", OperationTypeActivity, "ProcessPayment", full))

	// History missing one of the requirements = rule does not apply, default deny.
	partial := newHistory().
		startChunk("order-service").
		addTaskScheduled(1, "FraudCheckPassed").
		addTaskCompleted(1).
		build()
	assert.False(t, evalAllowed(cp, "order-service", OperationTypeActivity, "ProcessPayment", partial))

	// No history at all = default deny.
	assert.False(t, evalAllowed(cp, "order-service", OperationTypeActivity, "ProcessPayment", nil))
}

func TestEvaluateWithHistory_RequiresFallsThroughToOtherRules(t *testing.T) {
	// Rule with requires: only matches if conditions are met.
	// Wildcard allow rule (no requires): matches unconditionally.
	// When conditions are met, the more-specific rule should win.
	// When not met, the wildcard rule should still apply.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*",
					Action: wfaclapi.PolicyActionAllow,
				},
				{
					Type:   wfaclapi.WorkflowOperationTypeActivity,
					Name:   "Sensitive",
					Action: wfaclapi.PolicyActionDeny,
					Requires: []wfaclapi.RequiredEvent{
						{EventType: wfaclapi.RequiredEventTypeEvent, Status: wfaclapi.RequiredStatusRaised, Name: "DeleteApprovedByOps"},
					},
				},
			},
		}}),
	})

	// Without the approval event, the deny rule does not apply = fall back to wildcard allow.
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Sensitive", nil))

	// With the approval event, the deny rule applies and beats the wildcard at higher specificity.
	withApproval := newHistory().addEventRaised("DeleteApprovedByOps").build()
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Sensitive", withApproval))
}

func TestEvaluateWithHistory_RequiresAppIDFilter(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "order-service"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type:   wfaclapi.WorkflowOperationTypeActivity,
				Name:   "ProcessPayment",
				Action: wfaclapi.PolicyActionAllow,
				Requires: []wfaclapi.RequiredEvent{{
					EventType: wfaclapi.RequiredEventTypeActivity,
					Status:    wfaclapi.RequiredStatusCompleted,
					Name:      "FraudCheckPassed",
					AppID:     ptr.Of("fraud-service"),
				}},
			}},
		}}),
	})

	// Required event was produced by the right app.
	good := newHistory().
		startChunk("fraud-service").
		addTaskScheduled(1, "FraudCheckPassed").
		addTaskCompleted(1).
		build()
	assert.True(t, evalAllowed(cp, "order-service", OperationTypeActivity, "ProcessPayment", good))

	// Required event was produced by a different app — does not satisfy the requirement.
	wrong := newHistory().
		startChunk("evil-service").
		addTaskScheduled(1, "FraudCheckPassed").
		addTaskCompleted(1).
		build()
	assert.False(t, evalAllowed(cp, "order-service", OperationTypeActivity, "ProcessPayment", wrong))
}

func TestEvaluateWithHistory_RequiresActivityFailed(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type:   wfaclapi.WorkflowOperationTypeActivity,
				Name:   "Compensate",
				Action: wfaclapi.PolicyActionAllow,
				Requires: []wfaclapi.RequiredEvent{{
					EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusFailed, Name: "ChargeCard",
				}},
			}},
		}}),
	})

	failed := newHistory().
		addTaskScheduled(1, "ChargeCard").
		addTaskFailed(1).
		build()
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Compensate", failed))

	// Activity completed (not failed) = does not satisfy ActivityFailed requirement.
	completed := newHistory().
		addTaskScheduled(1, "ChargeCard").
		addTaskCompleted(1).
		build()
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Compensate", completed))
}

func TestEvaluateWithHistory_RequiresEventRaisedNameMustMatch(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type:   wfaclapi.WorkflowOperationTypeWorkflow,
				Name:   "Refund",
				Action: wfaclapi.PolicyActionAllow,
				Requires: []wfaclapi.RequiredEvent{{
					EventType: wfaclapi.RequiredEventTypeEvent, Status: wfaclapi.RequiredStatusRaised, Name: "ManagerApproved",
				}},
			}},
		}}),
	})

	approved := newHistory().addEventRaised("ManagerApproved").build()
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Refund", approved))

	wrongName := newHistory().addEventRaised("CustomerCancelled").build()
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "Refund", wrongName))
}

func TestEvaluateWithHistory_NoRequiresAlwaysApplies(t *testing.T) {
	// A rule with no Requires is unaffected by the absence of history.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*", Action: wfaclapi.PolicyActionAllow,
			}},
		}}),
	})
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Anything", nil))
}

func TestRequiresSatisfied_NilHistoryWithNoRequires(t *testing.T) {
	// No requirements means satisfied regardless of history.
	assert.True(t, requiresSatisfied(nil, nil))
	assert.True(t, requiresSatisfied([]wfaclapi.RequiredEvent{}, nil))
}

func TestRequiresSatisfied_NilHistoryWithRequires(t *testing.T) {
	assert.False(t, requiresSatisfied(
		[]wfaclapi.RequiredEvent{{
			EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "Prereq",
		}},
		nil,
	))
}

func TestRequiresSatisfied_ChunkBoundaryLookup(t *testing.T) {
	// Two chunks, second produced by a different app. Verify chunk lookup
	// resolves to the correct chunk for events spanning a boundary.
	hist := &protos.PropagatedHistory{
		Events: []*protos.HistoryEvent{
			{EventId: 1, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "A"}}},
			{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 1}}},
			{EventId: 3, EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "B"}}},
			{EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: 3}}},
		},
		Chunks: []*protos.PropagatedHistoryChunk{
			{AppId: "first-app", StartEventIndex: 0, EventCount: 2},
			{AppId: "second-app", StartEventIndex: 2, EventCount: 2},
		},
	}

	// Require completion of "B" produced by "second-app" — should match.
	assert.True(t, requiresSatisfied(
		[]wfaclapi.RequiredEvent{{
			EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "B", AppID: ptr.Of("second-app"),
		}},
		hist,
	))
	// Require completion of "B" produced by "first-app" — should not match.
	assert.False(t, requiresSatisfied(
		[]wfaclapi.RequiredEvent{{
			EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "B", AppID: ptr.Of("first-app"),
		}},
		hist,
	))
}

func TestEvaluate_BackwardCompatibleNoHistory(t *testing.T) {
	// Evaluate (no history) should preserve previous behavior for rules
	// without Requires.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type: wfaclapi.WorkflowOperationTypeWorkflow, Name: "WF", Action: wfaclapi.PolicyActionAllow,
			}},
		}}),
	})
	assert.True(t, evalAllowed(cp, "app-a", OperationTypeWorkflow, "WF", nil))
}

func TestEvaluateWithHistory_RequiresStartedAppliesUniformly(t *testing.T) {
	// `Started` should match across all event categories: TaskScheduled
	// (activity), ChildWorkflowInstanceCreated (child workflow), and
	// ExecutionStarted (own workflow). The category is selected by which
	// *Name field is set, with no name field, any of the three qualifies.

	t.Run("activity Started matches TaskScheduled", func(t *testing.T) {
		cp := Compile([]wfaclapi.WorkflowAccessPolicy{
			makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "Notify",
					Action: wfaclapi.PolicyActionAllow,
					Requires: []wfaclapi.RequiredEvent{{
						EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusStarted, Name: "ChargeCard",
					}},
				}},
			}}),
		})

		// Activity was scheduled (whether or not it completed).
		hist := newHistory().addTaskScheduled(1, "ChargeCard").build()
		assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Notify", hist))

		// No matching activity scheduled.
		hist2 := newHistory().addTaskScheduled(1, "OtherActivity").build()
		assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Notify", hist2))
	})

	t.Run("workflow Started matches ChildWorkflowInstanceCreated", func(t *testing.T) {
		cp := Compile([]wfaclapi.WorkflowAccessPolicy{
			makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "Notify",
					Action: wfaclapi.PolicyActionAllow,
					Requires: []wfaclapi.RequiredEvent{{
						EventType: wfaclapi.RequiredEventTypeWorkflow, Status: wfaclapi.RequiredStatusStarted, Name: "ChildOrder",
					}},
				}},
			}}),
		})

		hist := &protos.PropagatedHistory{
			Events: []*protos.HistoryEvent{{
				EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
					ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{Name: "ChildOrder"},
				},
			}},
			Chunks: []*protos.PropagatedHistoryChunk{{StartEventIndex: 0, EventCount: 1}},
		}
		assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Notify", hist))
	})

	t.Run("workflow Started matches ExecutionStarted", func(t *testing.T) {
		cp := Compile([]wfaclapi.WorkflowAccessPolicy{
			makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "Notify",
					Action: wfaclapi.PolicyActionAllow,
					Requires: []wfaclapi.RequiredEvent{{
						EventType: wfaclapi.RequiredEventTypeWorkflow, Status: wfaclapi.RequiredStatusStarted, Name: "ParentOrder",
					}},
				}},
			}}),
		})

		hist := &protos.PropagatedHistory{
			Events: []*protos.HistoryEvent{{
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{Name: "ParentOrder"},
				},
			}},
			Chunks: []*protos.PropagatedHistoryChunk{{StartEventIndex: 0, EventCount: 1}},
		}
		assert.True(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Notify", hist))
	})
}

func TestEnforceRequest_DenialReason(t *testing.T) {
	// EnforceRequest must populate Reason on its result so the orchestrator
	// can surface a distinct ErrorType to workflow authors.

	policy := []wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "order-service"}},
			Operations: []wfaclapi.WorkflowOperationRule{
				{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "ProcessPayment",
					Action: wfaclapi.PolicyActionAllow,
					Requires: []wfaclapi.RequiredEvent{{
						EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "FraudCheckPassed",
					}},
				},
				{
					Type: wfaclapi.WorkflowOperationTypeActivity, Name: "LogReceipt",
					Action: wfaclapi.PolicyActionAllow,
				},
			},
		}}),
	}
	cp := Compile(policy)

	t.Run("allow: requires satisfied = no reason", func(t *testing.T) {
		hist := newHistory().
			addTaskScheduled(1, "FraudCheckPassed").
			addTaskCompleted(1).
			build()
		allowed, reason := cp.Evaluate("order-service", OperationTypeActivity, "ProcessPayment", hist)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("deny: requires unmet = DenialReasonRequiresUnmet", func(t *testing.T) {
		allowed, reason := cp.Evaluate("order-service", OperationTypeActivity, "ProcessPayment", nil)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, reason,
			"a denial caused purely by missing prerequisite history must surface as RequiresUnmet")
	})

	t.Run("deny: caller not in any rule = DenialReasonNotAllowed", func(t *testing.T) {
		allowed, reason := cp.Evaluate("other-app", OperationTypeActivity, "ProcessPayment", nil)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonNotAllowed, reason)
	})

	t.Run("deny: no requires, plain not-allowed", func(t *testing.T) {
		// LogReceipt has no requires; calling it from an unknown caller
		// is a flat deny, not requires-unmet.
		allowed, reason := cp.Evaluate("other-app", OperationTypeActivity, "LogReceipt", nil)
		assert.False(t, allowed)
		assert.Equal(t, DenialReasonNotAllowed, reason)
	})

	t.Run("allow: another non-conditional rule matches even if a requires-rule was skipped", func(t *testing.T) {
		// Mixed policy: ProcessPayment has both a requires-gated rule and a
		// catch-all wildcard allow. When the prereq isn't met, the
		// wildcard rule wins = allow with no reason.
		mixed := Compile([]wfaclapi.WorkflowAccessPolicy{
			makePolicy("mixed", []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "order-service"}},
				Operations: []wfaclapi.WorkflowOperationRule{
					{
						Type: wfaclapi.WorkflowOperationTypeActivity, Name: "*",
						Action: wfaclapi.PolicyActionAllow,
					},
					{
						Type: wfaclapi.WorkflowOperationTypeActivity, Name: "ProcessPayment",
						Action: wfaclapi.PolicyActionAllow,
						Requires: []wfaclapi.RequiredEvent{{
							EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "FraudCheckPassed",
						}},
					},
				},
			}}),
		})
		allowed, reason := mixed.Evaluate("order-service", OperationTypeActivity, "ProcessPayment", nil)
		assert.True(t, allowed)
		assert.Equal(t, DenialReasonNone, reason)
	})

	t.Run("EnforceRequest result carries the reason", func(t *testing.T) {
		// Build a CreateWorkflowInstance request with no propagated history
		// for a workflow gated on requires. EnforceRequest should surface
		// DenialReasonRequiresUnmet on its result.
		gatedWF := Compile([]wfaclapi.WorkflowAccessPolicy{
			makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "order-service"}},
				Operations: []wfaclapi.WorkflowOperationRule{{
					Type:   wfaclapi.WorkflowOperationTypeWorkflow,
					Name:   "GatedWF",
					Action: wfaclapi.PolicyActionAllow,
					Requires: []wfaclapi.RequiredEvent{{
						EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "Prereq",
					}},
				}},
			}}),
		})

		req := &protos.CreateWorkflowInstanceRequest{
			StartEvent: &protos.HistoryEvent{
				EventType: &protos.HistoryEvent_ExecutionStarted{
					ExecutionStarted: &protos.ExecutionStartedEvent{
						Name:             "GatedWF",
						WorkflowInstance: &protos.WorkflowInstance{InstanceId: "i-1"},
					},
				},
			},
		}
		data, err := proto.Marshal(req)
		require.NoError(t, err)

		res, err := EnforceRequest(gatedWF, "order-service",
			"dapr.internal.default.target.workflow", "CreateWorkflowInstance", data)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.False(t, res.Allowed)
		assert.Equal(t, DenialReasonRequiresUnmet, res.Reason)
	})
}

func TestEvaluate_RuleWithRequiresIgnoredWithoutHistory(t *testing.T) {
	// A rule that depends on history must not match when callers go through
	// the no-history Evaluate path.
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy("test", []wfaclapi.WorkflowAccessPolicyRule{{
			Callers: []wfaclapi.WorkflowCaller{{AppID: "app-a"}},
			Operations: []wfaclapi.WorkflowOperationRule{{
				Type: wfaclapi.WorkflowOperationTypeActivity, Name: "Pay",
				Action: wfaclapi.PolicyActionAllow,
				Requires: []wfaclapi.RequiredEvent{{
					EventType: wfaclapi.RequiredEventTypeActivity, Status: wfaclapi.RequiredStatusCompleted, Name: "FraudCheckPassed",
				}},
			}},
		}}),
	})

	// Without history we cannot prove the requirement is met = default deny.
	assert.False(t, evalAllowed(cp, "app-a", OperationTypeActivity, "Pay", nil))
}
