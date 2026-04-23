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

package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

func validPolicy() *wfaclapi.WorkflowAccessPolicy {
	return &wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-policy", Namespace: "default"},
		Scoped:     common.Scoped{Scopes: []string{"app-a"}},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			DefaultAction: wfaclapi.PolicyActionDeny,
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
					Operations: []wfaclapi.WorkflowOperationRule{
						{
							Type:   wfaclapi.WorkflowOperationTypeWorkflow,
							Name:   "ProcessOrder",
							Action: wfaclapi.PolicyActionAllow,
						},
					},
				},
			},
		},
	}
}

func TestWorkflowAccessPolicy_ValidPolicy(t *testing.T) {
	err := WorkflowAccessPolicy(t.Context(), validPolicy())
	require.NoError(t, err)
}

func TestWorkflowAccessPolicy_ValidWithGlob(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations[0].Name = "Process*"
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_ValidAllowDefaultAction(t *testing.T) {
	p := validPolicy()
	p.Spec.DefaultAction = wfaclapi.PolicyActionAllow
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_ValidActivityType(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations[0].Type = wfaclapi.WorkflowOperationTypeActivity
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_ValidWithOperation(t *testing.T) {
	p := validPolicy()
	op := wfaclapi.WorkflowOperationSchedule
	p.Spec.Rules[0].Operations[0].Operation = &op
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_InvalidAction(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations[0].Action = wfaclapi.PolicyAction("bogus")
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
}

func TestWorkflowAccessPolicy_InvalidType(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations[0].Type = wfaclapi.WorkflowOperationType("bogus")
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_InvalidDefaultAction(t *testing.T) {
	p := validPolicy()
	p.Spec.DefaultAction = wfaclapi.PolicyAction("bogus")
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_InvalidOperation(t *testing.T) {
	p := validPolicy()
	op := wfaclapi.WorkflowOperation("bogus")
	p.Spec.Rules[0].Operations[0].Operation = &op
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyAppID(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Callers[0].AppID = ""
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyName(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations[0].Name = ""
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyCallers(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Callers = []wfaclapi.WorkflowCaller{}
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyOperations(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Operations = []wfaclapi.WorkflowOperationRule{}
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptySpec(t *testing.T) {
	// Empty spec with no rules should be valid (rules are optional).
	p := &wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "empty"},
		Spec:       wfaclapi.WorkflowAccessPolicySpec{},
	}
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_MultipleRulesOneInvalid(t *testing.T) {
	p := validPolicy()
	// Add a second rule with invalid action.
	p.Spec.Rules = append(p.Spec.Rules, wfaclapi.WorkflowAccessPolicyRule{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "other"}},
		Operations: []wfaclapi.WorkflowOperationRule{
			{Type: wfaclapi.WorkflowOperationType("bad"), Name: "wf", Action: wfaclapi.PolicyActionAllow},
		},
	})
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}
