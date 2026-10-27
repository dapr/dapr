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

package reconciler

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// The metadata API lists the compstore, and tests treat a listed policy as
// enforced: the compiled set must be swapped before the store changes.
func Test_workflowAccessPolicies_recompileBeforeStore(t *testing.T) {
	t.Parallel()

	store := compstore.New()
	names := func() []string {
		listed := store.ListWorkflowAccessPolicies()
		out := make([]string, 0, len(listed))
		for _, p := range listed {
			out = append(out, p.Name)
		}
		return out
	}

	var listedAtSwap [][]string
	var callerListed []bool
	w := &workflowAccessPolicies{
		appID: "target",
		store: store,
		recompiler: func(cp *workflowacl.CompiledPolicies) {
			listedAtSwap = append(listedAtSwap, names())
			callerListed = append(callerListed, cp.ListsCaller("caller"))
		},
	}

	policy := testPolicy("p1", "caller")

	require.NoError(t, w.update(t.Context(), policy))
	assert.Equal(t, []string{"p1"}, names())
	require.NoError(t, w.delete(t.Context(), policy))
	assert.Empty(t, names())

	require.Len(t, listedAtSwap, 2)
	assert.Empty(t, listedAtSwap[0], "the policy must be enforced before the store lists it")
	assert.Equal(t, []string{"p1"}, listedAtSwap[1], "the store must list the policy until it is no longer enforced")
	assert.Equal(t, []bool{true, false}, callerListed)
}

// Reconciles of a batch run concurrently; every policy of the batch must be
// in the enforced set once the batch is done.
func Test_workflowAccessPolicies_concurrentUpdates(t *testing.T) {
	t.Parallel()

	store := compstore.New()
	var mu sync.Mutex
	var last *workflowacl.CompiledPolicies
	w := &workflowAccessPolicies{
		appID: "target",
		store: store,
		recompiler: func(cp *workflowacl.CompiledPolicies) {
			mu.Lock()
			defer mu.Unlock()
			last = cp
		},
	}

	const n = 8
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			assert.NoError(t, w.update(t.Context(), testPolicy(fmt.Sprintf("p%d", i), fmt.Sprintf("caller%d", i))))
		})
	}
	wg.Wait()

	require.Len(t, store.ListWorkflowAccessPolicies(), n)
	for i := range n {
		assert.Truef(t, last.ListsCaller(fmt.Sprintf("caller%d", i)), "caller%d missing from the enforced set", i)
	}
}

func testPolicy(name, caller string) wfaclapi.WorkflowAccessPolicy {
	return wfaclapi.WorkflowAccessPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "dapr.io/v1alpha1", Kind: "WorkflowAccessPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: wfaclapi.WorkflowAccessPolicySpec{Rules: []wfaclapi.WorkflowAccessPolicyRule{{
			Callers:   []wfaclapi.WorkflowCaller{{AppID: caller}},
			Workflows: []wfaclapi.WorkflowRule{{Name: "wf", Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationSchedule}}},
		}}},
	}
}
