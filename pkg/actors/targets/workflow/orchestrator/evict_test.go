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

package orchestrator

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	workflowacl "github.com/dapr/dapr/pkg/acl/workflow"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
)

func evictRequest(h *createHarness, callerAppID string) *internalsv1pb.InternalInvokeRequest {
	req := internalsv1pb.NewInternalInvokeRequest(todo.EvictWorkflowMethod).
		WithActor(h.orch.actorType, h.orch.actorID)
	if callerAppID != "" {
		req = req.WithMetadata(map[string][]string{invokev1.CallerIDHeader: {callerAppID}})
	}
	return req
}

// A force purge on another daprd evicts the owner's actor: its cache is
// dropped and the actor leaves the table, so the next call loads from the
// store.
func Test_evict_deactivatesResidentActor(t *testing.T) {
	const instanceID = "test-evict"

	h := newCreateHarness(t, instanceID)
	h.primeHistory(startEventFor(instanceID, time.Now(), withExecutionID("exec-1")), completedEvent())
	require.True(t, h.orch.Exists(instanceID))

	res, err := h.orch.InvokeMethod(t.Context(), evictRequest(h, ""))
	require.NoError(t, err)
	assert.Equal(t, int32(http.StatusOK), res.GetStatus().GetCode())

	assert.Nil(t, h.orch.state, "the cache must be dropped")
	assert.True(t, h.orch.isRetired())
	assert.False(t, h.orch.Exists(instanceID), "the actor must leave the table")

	// The purging daprd evicts twice, before and after the delete: a second
	// eviction of a retired actor answers success rather than waiting on its
	// closed lock.
	res, err = h.orch.InvokeMethod(t.Context(), evictRequest(h, ""))
	require.NoError(t, err)
	assert.Equal(t, int32(http.StatusOK), res.GetStatus().GetCode())
}

// Eviction is an internal method: with access policies on, only this app's
// daprds may send it.
func Test_evict_accessPolicy(t *testing.T) {
	const instanceID = "test-evict-acl"

	newHarness := func(t *testing.T) *createHarness {
		h := newCreateHarness(t, instanceID)
		holder := workflowacl.NewHolder()
		holder.Store(workflowacl.Compile([]wfaclapi.WorkflowAccessPolicy{{
			Spec: wfaclapi.WorkflowAccessPolicySpec{Rules: []wfaclapi.WorkflowAccessPolicyRule{{
				Callers: []wfaclapi.WorkflowCaller{{AppID: "other-app"}},
				Workflows: []wfaclapi.WorkflowRule{{
					Name:       "*",
					Operations: []wfaclapi.WorkflowOperation{wfaclapi.WorkflowOperationPurge},
				}},
			}}},
		}}))
		h.orch.workflowAccessPolicies = holder
		h.primeHistory(startEventFor(instanceID, time.Now(), withExecutionID("exec-1")), completedEvent())
		return h
	}

	t.Run("another app is refused", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.orch.InvokeMethod(t.Context(), evictRequest(h, "other-app"))
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err))
		assert.NotNil(t, h.orch.state, "a refused eviction keeps the actor")
		assert.False(t, h.orch.isRetired())
	})

	t.Run("this app evicts", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.orch.InvokeMethod(t.Context(), evictRequest(h, "testapp"))
		require.NoError(t, err)
		assert.True(t, h.orch.isRetired())
	})
}
