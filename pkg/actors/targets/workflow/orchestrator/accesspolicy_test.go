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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// Under a deny-all policy, a completion from another app is admitted only
// when this workflow dispatched the task to that app.
func Test_checkAccessPolicy_completionSender(t *testing.T) {
	const instanceID = "test-completion-sender"
	appB := "app-b"

	newHarness := func(t *testing.T, prime bool) *createHarness {
		h := newCreateHarness(t, instanceID)
		holder := workflowacl.NewHolder()
		holder.Store(workflowacl.Compile([]wfaclapi.WorkflowAccessPolicy{{}}))
		h.orch.workflowAccessPolicies = holder
		if prime {
			remoteTask := taskScheduledEvent(1)
			remoteTask.Router = &protos.TaskRouter{SourceAppID: h.orch.appID, TargetAppID: &appB}
			remoteChild := childCreatedEvent(2)
			remoteChild.Router = &protos.TaskRouter{SourceAppID: h.orch.appID, TargetAppID: &appB}
			h.primeHistory(startEventFor(instanceID, time.Now(), withExecutionID("exec-1")),
				taskScheduledEvent(0), remoteTask, remoteChild, timerCreatedEvent(3))
		}
		return h
	}

	childFailed := &backend.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{
		ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 2},
	}}

	check := func(h *createHarness, caller string, ev *backend.HistoryEvent) error {
		var md map[string]*internalsv1pb.ListStringValue
		if caller != "" {
			md = map[string]*internalsv1pb.ListStringValue{invokev1.CallerIDHeader: {Values: []string{caller}}}
		}
		return h.orch.checkAccessPolicy(t.Context(), todo.AddWorkflowEventMethod, nil, ev, nil, md)
	}

	for name, tc := range map[string]struct {
		caller  string
		self    bool
		ev      *backend.HistoryEvent
		allowed bool
	}{
		"activity result from the app it was dispatched to":      {caller: appB, ev: taskCompletedEvent(1), allowed: true},
		"activity failure from the app it was dispatched to":     {caller: appB, ev: taskFailedEvent(1), allowed: true},
		"child result from the app it was dispatched to":         {caller: appB, ev: childCompletedEvent(2), allowed: true},
		"child failure from the app it was dispatched to":        {caller: appB, ev: childFailed, allowed: true},
		"result from this app":                                   {self: true, ev: taskCompletedEvent(1), allowed: true},
		"activity result from another app":                       {caller: "app-c", ev: taskCompletedEvent(1)},
		"child result from another app":                          {caller: "app-c", ev: childCompletedEvent(2)},
		"child failure from another app":                         {caller: "app-c", ev: childFailed},
		"dispatched app answering a task run by this app":        {caller: appB, ev: taskCompletedEvent(0)},
		"dispatched app answering a task that was never sent":    {caller: appB, ev: taskCompletedEvent(9)},
		"dispatched app answering with a timer's id":             {caller: appB, ev: taskCompletedEvent(3)},
		"missing caller identity for a task run by this app":     {caller: "", ev: taskCompletedEvent(0)},
		"missing caller identity for a task sent to another app": {caller: "", ev: taskCompletedEvent(1)},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, true)
			if tc.self {
				tc.caller = h.orch.appID
			}
			err := check(h, tc.caller, tc.ev)
			if tc.allowed {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
			assert.Contains(t, err.Error(), workflowacl.DeniedMessageBase)
		})
	}

	t.Run("a timer firing from another app is still malformed", func(t *testing.T) {
		err := check(newHarness(t, true), appB, firedEvent(3))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "malformed request")
	})

	// Nothing to complete: admission answers not found so the sender stops.
	t.Run("completion for an instance that does not exist passes to admission", func(t *testing.T) {
		require.NoError(t, check(newHarness(t, false), "app-c", taskCompletedEvent(1)))
	})
}
