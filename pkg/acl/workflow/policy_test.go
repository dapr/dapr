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

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

const (
	opSchedule  = wfaclapi.WorkflowOperationSchedule
	opTerminate = wfaclapi.WorkflowOperationTerminate
	opPurge     = wfaclapi.WorkflowOperationPurge
	opGet       = wfaclapi.WorkflowOperationGet
)

var allOps = []wfaclapi.WorkflowOperation{
	wfaclapi.WorkflowOperationSchedule,
	wfaclapi.WorkflowOperationTerminate,
	wfaclapi.WorkflowOperationRaise,
	wfaclapi.WorkflowOperationPause,
	wfaclapi.WorkflowOperationResume,
	wfaclapi.WorkflowOperationPurge,
	wfaclapi.WorkflowOperationGet,
	wfaclapi.WorkflowOperationRerun,
}

func makePolicy(rules ...wfaclapi.WorkflowAccessPolicyRule) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		Spec: wfaclapi.WorkflowAccessPolicySpec{Rules: rules},
	}
}

func makePolicyWithDefault(action wfaclapi.PolicyAction, rules ...wfaclapi.WorkflowAccessPolicyRule) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: action,
			Rules:         rules,
		},
	}
}

func wfRule(name string, action wfaclapi.PolicyAction, ops ...wfaclapi.WorkflowOperation) wfaclapi.WorkflowRule {
	if len(ops) == 0 {
		ops = []wfaclapi.WorkflowOperation{opSchedule}
	}
	return wfaclapi.WorkflowRule{Name: name, Operations: ops, Action: action}
}

func actRule(name string, action wfaclapi.PolicyAction) wfaclapi.ActivityRule {
	return wfaclapi.ActivityRule{Name: name, Action: action}
}

func callerRule(callers []string, wfs []wfaclapi.WorkflowRule, acts []wfaclapi.ActivityRule) wfaclapi.WorkflowAccessPolicyRule {
	cs := make([]wfaclapi.WorkflowCaller, len(callers))
	for i, c := range callers {
		cs[i] = wfaclapi.WorkflowCaller{AppID: c}
	}
	return wfaclapi.WorkflowAccessPolicyRule{Callers: cs, Workflows: wfs, Activities: acts}
}

func TestCompile_NilWhenNoPolicies(t *testing.T) {
	assert.Nil(t, Compile(nil))
	assert.Nil(t, Compile([]wfaclapi.WorkflowAccessPolicy{}))
}

func TestEvaluate_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.Evaluate("any-app", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_DefaultDenyWhenPoliciesExist(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy()})
	assert.False(t, cp.Evaluate("any-app", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_AllowSpecificCaller(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"checkout"}, []wfaclapi.WorkflowRule{
			wfRule("ProcessOrder", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("checkout", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.False(t, cp.Evaluate("other-app", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeWorkflow, opSchedule, "OtherWorkflow"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeActivity, opSchedule, "ProcessOrder"))
}

func TestEvaluate_PerOperationGranularity(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("OrderWF", wfaclapi.PolicyActionAllow, opSchedule, opTerminate),
			wfRule("OrderWF", wfaclapi.PolicyActionDeny, opPurge),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OrderWF"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opTerminate, "OrderWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opPurge, "OrderWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opGet, "OrderWF"))
}

func TestEvaluate_OperationNotInRuleFallsBackToDefault(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicyWithDefault(wfaclapi.PolicyActionAllow,
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("WF", wfaclapi.PolicyActionDeny, opPurge),
		}, nil),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opPurge, "WF"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "WF"))
}

func TestEvaluate_GlobPatterns(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"},
			[]wfaclapi.WorkflowRule{wfRule("Process*", wfaclapi.PolicyActionAllow, allOps...)},
			[]wfaclapi.ActivityRule{actRule("*", wfaclapi.PolicyActionAllow)},
		),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opTerminate, "ProcessRefund"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "CancelOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeActivity, opSchedule, "AnyActivity"))
}

func TestEvaluate_MostSpecificWins(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionDeny, allOps...),
			wfRule("Process*", wfaclapi.PolicyActionAllow, allOps...),
			wfRule("ProcessSecret", wfaclapi.PolicyActionDeny, allOps...),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ProcessSecret"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "CancelOrder"))
}

func TestEvaluate_DenyWinsTies(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("Order*", wfaclapi.PolicyActionAllow),
		}, nil),
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("Order*", wfaclapi.PolicyActionDeny),
		}, nil),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OrderProcess"))
}

func TestEvaluate_MultipleCallers(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a", "app-b"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "Any"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, opSchedule, "Any"))
	assert.False(t, cp.Evaluate("app-c", OperationTypeWorkflow, opSchedule, "Any"))
}

func TestEvaluate_MultiplePoliciesMerged(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy(callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("WorkflowA", wfaclapi.PolicyActionAllow),
		}, nil)),
		makePolicy(callerRule([]string{"app-b"}, []wfaclapi.WorkflowRule{
			wfRule("WorkflowB", wfaclapi.PolicyActionAllow),
		}, nil)),
	})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "WorkflowA"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "WorkflowB"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, opSchedule, "WorkflowB"))
	assert.False(t, cp.Evaluate("app-b", OperationTypeWorkflow, opSchedule, "WorkflowA"))
}

func TestEvaluate_InvalidGlobSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("[invalid", wfaclapi.PolicyActionAllow),
			wfRule("ValidWorkflow", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ValidWorkflow"))
}

func TestEvaluate_DefaultActionAllow(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicyWithDefault(wfaclapi.PolicyActionAllow,
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("SpecificWF", wfaclapi.PolicyActionDeny),
		}, nil),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "SpecificWF"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OtherWF"))
}

func TestEvaluate_DefaultActionDeny(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicyWithDefault(wfaclapi.PolicyActionDeny,
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("AllowedWF", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "AllowedWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OtherWF"))
}

func TestEvaluate_DefaultActionEmptyIsDeny(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{{
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: "",
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
					wfRule("AllowedWF", wfaclapi.PolicyActionAllow),
				}, nil),
			},
		},
	}})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "AllowedWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OtherWF"))
}

func TestEvaluate_MultiplePoliciesDenyWinsDefault(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicyWithDefault(wfaclapi.PolicyActionAllow),
		makePolicyWithDefault(wfaclapi.PolicyActionDeny),
	})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_EmptyCallersSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		wfaclapi.WorkflowAccessPolicyRule{
			Callers: []wfaclapi.WorkflowCaller{},
			Workflows: []wfaclapi.WorkflowRule{
				wfRule("*", wfaclapi.PolicyActionAllow),
			},
		},
	)})

	assert.False(t, cp.Evaluate("any-app", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_TypeIsolation(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, nil, []wfaclapi.ActivityRule{
			actRule("*", wfaclapi.PolicyActionAllow),
		}),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "AnyWorkflow"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeActivity, opSchedule, "AnyActivity"))
}

func TestEvaluate_BroadAllowWithSpecificDeny(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a", "app-b"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionAllow, allOps...),
		}, nil),
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("SecretWF", wfaclapi.PolicyActionDeny, allOps...),
		}, nil),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "SecretWF"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "PublicWF"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, opSchedule, "SecretWF"))
	assert.False(t, cp.Evaluate("app-c", OperationTypeWorkflow, opSchedule, "PublicWF"))
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

func TestIsCallerKnown_NilPoliciesAllowAll(t *testing.T) {
	var cp *CompiledPolicies
	assert.True(t, cp.IsCallerKnown("any-app", OperationTypeWorkflow))
}

func TestIsCallerKnown_SpecificAllowOnlyIsNotKnown(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("WF1", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.False(t, cp.IsCallerKnown("app-a", OperationTypeWorkflow))
}

func TestIsCallerKnown_WildcardAllowIsKnown(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"trusted"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionAllow),
		}, nil),
	)})

	assert.True(t, cp.IsCallerKnown("trusted", OperationTypeWorkflow))
	assert.False(t, cp.IsCallerKnown("trusted", OperationTypeActivity))
}

func TestIsCallerKnown_WildcardAllowPlusSpecificDenyIsNotKnown(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionAllow),
			wfRule("SecretWF", wfaclapi.PolicyActionDeny),
		}, nil),
	)})

	assert.False(t, cp.IsCallerKnown("app-a", OperationTypeWorkflow))
}

func TestIsCallerKnown_DenyOnlyIsNotKnown(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"deny-only"}, []wfaclapi.WorkflowRule{
			wfRule("*", wfaclapi.PolicyActionDeny),
		}, nil),
	)})

	assert.False(t, cp.IsCallerKnown("deny-only", OperationTypeWorkflow))
}

func TestIsCallerKnown_ActivityWildcardAllow(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"trusted"}, nil, []wfaclapi.ActivityRule{
			actRule("*", wfaclapi.PolicyActionAllow),
		}),
	)})

	assert.True(t, cp.IsCallerKnown("trusted", OperationTypeActivity))
	assert.False(t, cp.IsCallerKnown("trusted", OperationTypeWorkflow))
}
