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

func wfRule(name string, ops ...wfaclapi.WorkflowOperation) wfaclapi.WorkflowRule {
	if len(ops) == 0 {
		ops = []wfaclapi.WorkflowOperation{opSchedule}
	}
	return wfaclapi.WorkflowRule{Name: name, Operations: ops}
}

func actRule(name string) wfaclapi.ActivityRule {
	return wfaclapi.ActivityRule{Name: name}
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

func TestEvaluate_PoliciesPresentDefaultDeny(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy()})
	assert.False(t, cp.Evaluate("any-app", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_MatchingRuleAllows(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"checkout"}, []wfaclapi.WorkflowRule{
			wfRule("ProcessOrder"),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("checkout", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.False(t, cp.Evaluate("other-app", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeWorkflow, opSchedule, "OtherWorkflow"))
	assert.False(t, cp.Evaluate("checkout", OperationTypeActivity, opSchedule, "ProcessOrder"))
}

func TestEvaluate_OperationGranularity(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("OrderWF", opSchedule, opTerminate),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "OrderWF"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opTerminate, "OrderWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opPurge, "OrderWF"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opGet, "OrderWF"))
}

func TestEvaluate_GlobAndExactPatterns(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"},
			[]wfaclapi.WorkflowRule{
				wfRule("Process*", allOps...),
				wfRule("Exact", opSchedule),
			},
			[]wfaclapi.ActivityRule{actRule("*")},
		),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ProcessOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opTerminate, "ProcessRefund"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "Exact"))
	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "CancelOrder"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeActivity, opSchedule, "AnyActivity"))
}

func TestEvaluate_MultipleCallers(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a", "app-b"}, []wfaclapi.WorkflowRule{
			wfRule("*"),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "Any"))
	assert.True(t, cp.Evaluate("app-b", OperationTypeWorkflow, opSchedule, "Any"))
	assert.False(t, cp.Evaluate("app-c", OperationTypeWorkflow, opSchedule, "Any"))
}

func TestEvaluate_MultiplePoliciesMerged(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{
		makePolicy(callerRule([]string{"app-a"}, []wfaclapi.WorkflowRule{
			wfRule("WorkflowA"),
		}, nil)),
		makePolicy(callerRule([]string{"app-b"}, []wfaclapi.WorkflowRule{
			wfRule("WorkflowB"),
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
			wfRule("[invalid"),
			wfRule("ValidWorkflow"),
		}, nil),
	)})

	assert.True(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "ValidWorkflow"))
}

func TestEvaluate_EmptyCallersSkipped(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		wfaclapi.WorkflowAccessPolicyRule{
			Callers: []wfaclapi.WorkflowCaller{},
			Workflows: []wfaclapi.WorkflowRule{
				wfRule("*"),
			},
		},
	)})

	assert.False(t, cp.Evaluate("any-app", OperationTypeWorkflow, opSchedule, "AnyWF"))
}

func TestEvaluate_TypeIsolation(t *testing.T) {
	cp := Compile([]wfaclapi.WorkflowAccessPolicy{makePolicy(
		callerRule([]string{"app-a"}, nil, []wfaclapi.ActivityRule{actRule("*")}),
	)})

	assert.False(t, cp.Evaluate("app-a", OperationTypeWorkflow, opSchedule, "AnyWorkflow"))
	assert.True(t, cp.Evaluate("app-a", OperationTypeActivity, opSchedule, "AnyActivity"))
}
