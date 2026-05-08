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
			Rules: []wfaclapi.WorkflowAccessPolicyRule{
				{
					Callers: []wfaclapi.WorkflowCaller{{AppID: "caller-app"}},
					Workflows: []wfaclapi.WorkflowRule{
						{
							Name:       "ProcessOrder",
							Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule},
						},
					},
				},
			},
		},
	}
}

func TestWorkflowAccessPolicy_ValidPolicy(t *testing.T) {
	require.NoError(t, WorkflowAccessPolicy(t.Context(), validPolicy()))
}

func TestWorkflowAccessPolicy_ValidWithGlob(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows[0].Name = "Process*"
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_ValidActivityRule(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows = nil
	p.Spec.Rules[0].Activities = []wfaclapi.ActivityRule{
		{Name: "ChargePayment"},
	}
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_AllNewWorkflowOperations(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows[0].Operations = []wfaclapi.WorkflowOperation{
		wfaclapi.WorkflowOperationSchedule,
		wfaclapi.WorkflowOperationTerminate,
		wfaclapi.WorkflowOperationRaise,
		wfaclapi.WorkflowOperationPause,
		wfaclapi.WorkflowOperationResume,
		wfaclapi.WorkflowOperationPurge,
		wfaclapi.WorkflowOperationGet,
		wfaclapi.WorkflowOperationRerun,
	}
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_BothWorkflowsAndActivities(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Activities = []wfaclapi.ActivityRule{
		{Name: "ChargePayment"},
	}
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_InvalidOperation(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows[0].Operations = []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperation("bogus")}
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyAppID(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Callers[0].AppID = ""
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptyWorkflowName(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows[0].Name = ""
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
	p.Spec.Rules[0].Workflows[0].Operations = []wfaclapi.WorkflowOperation{}
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_NeitherWorkflowsNorActivities(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules[0].Workflows = nil
	p.Spec.Rules[0].Activities = nil
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}

func TestWorkflowAccessPolicy_EmptySpec(t *testing.T) {
	p := &wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: "empty"},
		Spec:       wfaclapi.WorkflowAccessPolicySpec{},
	}
	require.NoError(t, WorkflowAccessPolicy(t.Context(), p))
}

func TestWorkflowAccessPolicy_MultipleRulesOneInvalid(t *testing.T) {
	p := validPolicy()
	p.Spec.Rules = append(p.Spec.Rules, wfaclapi.WorkflowAccessPolicyRule{
		Callers: []wfaclapi.WorkflowCaller{{AppID: "other"}},
		Workflows: []wfaclapi.WorkflowRule{
			{Name: "wf", Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperation("bad")}},
		},
	})
	err := WorkflowAccessPolicy(t.Context(), p)
	require.Error(t, err)
}
