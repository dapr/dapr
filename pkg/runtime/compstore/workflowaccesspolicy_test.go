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

package compstore

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

func makeWFPolicy(name string) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func makeWFPolicyWithCaller(name, callerAppID string) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: wfaclapi.WorkflowAccessPolicySpec{
			Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: callerAppID}},
			}},
		},
	}
}

func TestWorkflowAccessPolicy_AddAndList(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p2"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p3"))

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 3)
	assert.Equal(t, "p1", list[0].Name)
	assert.Equal(t, "p2", list[1].Name)
	assert.Equal(t, "p3", list[2].Name)
}

func TestWorkflowAccessPolicy_AddReplace(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicyWithCaller("p1", "first"))
	s.AddWorkflowAccessPolicy(makeWFPolicyWithCaller("p1", "second"))

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 1)
	assert.Equal(t, "p1", list[0].Name)
	assert.Equal(t, "second", list[0].Spec.Rules[0].Callers[0].AppID)
}

func TestWorkflowAccessPolicy_Delete(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p2"))
	s.DeleteWorkflowAccessPolicy("p1")

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 1)
	assert.Equal(t, "p2", list[0].Name)
}

func TestWorkflowAccessPolicy_DeleteNonExistent(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.DeleteWorkflowAccessPolicy("nonexistent")

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 1)
	assert.Equal(t, "p1", list[0].Name)
}

func TestWorkflowAccessPolicy_DeleteFromEmpty(t *testing.T) {
	s := New()
	s.DeleteWorkflowAccessPolicy("nonexistent")

	list := s.ListWorkflowAccessPolicies()
	assert.Empty(t, list)
}

func TestWorkflowAccessPolicy_EmptyList(t *testing.T) {
	s := New()
	list := s.ListWorkflowAccessPolicies()
	assert.NotNil(t, list)
	assert.Empty(t, list)
}

func TestWorkflowAccessPolicy_ListReturnsCopy(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p2"))

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 2)

	// Mutate the returned slice.
	list[0].Name = "mutated"

	// Store should be unaffected.
	list2 := s.ListWorkflowAccessPolicies()
	assert.Equal(t, "p1", list2[0].Name)
}

func TestWorkflowAccessPolicy_AddMultipleSameName(t *testing.T) {
	s := New()
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))
	s.AddWorkflowAccessPolicy(makeWFPolicy("p1"))

	list := s.ListWorkflowAccessPolicies()
	require.Len(t, list, 1)
}

func TestWorkflowAccessPolicy_ConcurrentAccess(t *testing.T) {
	s := New()

	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(3)
		name := "p" + string(rune('0'+i))

		go func() {
			defer wg.Done()
			s.AddWorkflowAccessPolicy(makeWFPolicy(name))
		}()
		go func() {
			defer wg.Done()
			s.DeleteWorkflowAccessPolicy(name)
		}()
		go func() {
			defer wg.Done()
			_ = s.ListWorkflowAccessPolicies()
		}()
	}

	wg.Wait()
	// No assertion needed — test passes if no race detector failure.
}
