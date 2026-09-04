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
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	actorreminders "github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

const (
	notifyChildID  = "notify-child"
	notifyParentID = "notify-parent"
	notifyTaskID   = int32(2)
)

func notifyStartEvent() *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "Child",
				Input:            wrapperspb.String(`"in"`),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID},
				ParentInstance: &protos.ParentInstanceInfo{
					TaskScheduledId:  notifyTaskID,
					WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyParentID, ExecutionId: wrapperspb.String("parent-exec-1")},
					AppID:            new("testapp"),
				},
			},
		},
	}
}

func notifyCompletedEvent(status protos.OrchestrationStatus) *backend.HistoryEvent {
	return &backend.HistoryEvent{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{
			ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: status,
				Result:         wrapperspb.String(`"out"`),
				FailureDetails: &protos.TaskFailureDetails{ErrorMessage: "boom"},
			},
		},
	}
}

// notifyHarness records saves (tagged with the parent-notify operation they
// carry), parent AddWorkflowEvent calls and reminder creates, in order.
type notifyHarness struct {
	lock      sync.Mutex
	ops       []string
	calls     []*internalv1pb.InternalInvokeRequest
	callErr   error
	purged    atomic.Bool
	staleETag atomic.Bool
	rows      map[string][]byte
	orch      *orchestrator
}

func (h *notifyHarness) saveTag(req *actorapi.TransactionalRequest) string {
	for _, op := range req.Operations {
		switch r := op.Request.(type) {
		case actorapi.TransactionalUpsert:
			if r.Key == "parent-notify" {
				return "save+notify"
			}
		case actorapi.TransactionalDelete:
			if r.Key == "parent-notify" {
				return "save-notify"
			}
		}
	}
	return "save"
}

func newNotifyHarness(t *testing.T, history, inbox []*backend.HistoryEvent, pending bool, scheduler func(context.Context, *backend.WorkflowWorkItem) error) *notifyHarness {
	t.Helper()
	h := &notifyHarness{rows: map[string][]byte{}}

	meta, err := proto.Marshal(&backend.BackendWorkflowStateMetadata{Generation: 1, InboxLength: uint64(len(inbox)), HistoryLength: uint64(len(history))})
	require.NoError(t, err)
	for i, e := range history {
		data, merr := proto.Marshal(e)
		require.NoError(t, merr)
		h.rows[fmt.Sprintf("history-%06d", i)] = data
	}
	for i, e := range inbox {
		data, merr := proto.Marshal(e)
		require.NoError(t, merr)
		h.rows[fmt.Sprintf("inbox-%06d", i)] = data
	}
	if pending {
		h.rows["parent-notify"] = []byte{1}
	}
	etag := "etag"

	fakeState := statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			if req.Key == wfenginestate.MetadataKey {
				if h.purged.Load() {
					return &actorapi.StateResponse{}, nil
				}
				if h.staleETag.Load() {
					stale := "etag-moved"
					return &actorapi.StateResponse{Data: meta, ETag: &stale}, nil
				}
				return &actorapi.StateResponse{Data: meta, ETag: &etag}, nil
			}
			return &actorapi.StateResponse{}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			res := make(actorapi.BulkStateResponse, len(req.Keys))
			for _, k := range req.Keys {
				entry := actorapi.BulkStateEntry{Data: h.rows[k]}
				if len(entry.Data) > 0 {
					entry.ETag = &etag
				}
				res[k] = entry
			}
			return res, nil
		}).
		WithTransactionalStateOperationFn(func(_ context.Context, _ bool, req *actorapi.TransactionalRequest, _ bool) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, h.saveTag(req))
			for _, op := range req.Operations {
				switch r := op.Request.(type) {
				case actorapi.TransactionalUpsert:
					if b, ok := r.Value.([]byte); ok {
						h.rows[r.Key] = b
					}
				case actorapi.TransactionalDelete:
					delete(h.rows, r.Key)
				}
			}
			return nil
		})

	fakeRems := remindersfake.New().WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
		h.lock.Lock()
		defer h.lock.Unlock()
		h.ops = append(h.ops, "create:"+req.Name)
		return nil
	})

	fakeRouter := routerfake.New().WithCallFn(func(_ context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
		h.lock.Lock()
		defer h.lock.Unlock()
		h.ops = append(h.ops, "call:"+req.GetMessage().GetMethod())
		h.calls = append(h.calls, req)
		if h.callErr != nil {
			return nil, h.callErr
		}
		return &internalv1pb.InternalInvokeResponse{Status: &internalv1pb.Status{Code: http.StatusOK}}, nil
	})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		Scheduler:         scheduler,
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithReminders(func(context.Context) (actorreminders.Interface, error) { return fakeRems, nil }).
			WithState(func(context.Context) (actorstate.Interface, error) { return fakeState, nil }).
			WithRouter(func(context.Context) (router.Interface, error) { return fakeRouter, nil }),
	})
	require.NoError(t, err)
	h.orch = fact.GetOrCreate(notifyChildID).(*orchestrator)
	return h
}

func (h *notifyHarness) snapshot() []string {
	h.lock.Lock()
	defer h.lock.Unlock()
	return append([]string(nil), h.ops...)
}

func Test_runWorkflow_terminalTurnSavesBeforeParentNotify(t *testing.T) {
	t.Parallel()

	newCompletingHarness := func(t *testing.T) *notifyHarness {
		start := notifyStartEvent()
		history := []*backend.HistoryEvent{{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		}, start}
		inbox := []*backend.HistoryEvent{{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "go"}},
		}}
		completed := notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
		scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
			wi.State.CompletedEvent = completed.GetExecutionCompleted()
			wi.State.NewEvents = append(wi.State.NewEvents, completed)
			wi.State.PendingMessages = []*backend.WorkflowRuntimeStateMessage{{
				TargetInstanceId: notifyParentID,
				HistoryEvent: &backend.HistoryEvent{
					EventId: -1, Timestamp: timestamppb.Now(),
					Router: &protos.TaskRouter{SourceAppID: "testapp", TargetAppID: new("testapp")},
					EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
						TaskScheduledId: notifyTaskID, Result: wrapperspb.String(`"out"`),
					}},
				},
			}}
			wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
			return nil
		}
		h := newNotifyHarness(t, history, inbox, false, scheduler)
		st := wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "dapr.internal.default.testapp.workflow", ActivityActorType: "dapr.internal.default.testapp.activity"})
		for _, e := range history {
			st.AddToHistory(e)
		}
		for _, e := range inbox {
			st.AddToInbox(e)
		}
		h.orch.state = st
		h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)
		h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, start.GetExecutionStarted())
		return h
	}

	t.Run("commit, notify, then clear the marker", func(t *testing.T) {
		t.Parallel()
		h := newCompletingHarness(t)
		completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-1"})
		require.NoError(t, err)
		assert.Equal(t, todo.RunCompletedTrue, completed)
		assert.Equal(t, []string{"save+notify", "call:" + todo.AddWorkflowEventMethod, "save-notify"}, h.snapshot())
		assert.False(t, h.orch.state.ParentNotifyPending, "the parent acknowledged")
		require.Len(t, h.calls, 1)
		assert.Equal(t, notifyChildID, h.calls[0].GetMetadata()[todo.MetadataSenderInstanceID].GetValues()[0])
		assert.Equal(t, "parent-exec-1", h.calls[0].GetMetadata()[todo.MetadataParentExecutionID].GetValues()[0])
	})

	t.Run("fastpath asserts the janitor before the terminal save", func(t *testing.T) {
		t.Parallel()
		h := newCompletingHarness(t)
		h.orch.fastPath = true
		completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-1"})
		require.NoError(t, err)
		assert.Equal(t, todo.RunCompletedTrue, completed)
		ops := h.snapshot()
		require.GreaterOrEqual(t, len(ops), 3)
		assert.Equal(t, []string{"create:" + janitorReminderName, "save+notify", "call:" + todo.AddWorkflowEventMethod}, ops[:3], "a local wake has no reminder to nack, so the janitor must exist before the save")
	})

	t.Run("a purge landing on the terminal save aborts before the notify", func(t *testing.T) {
		t.Parallel()
		h := newCompletingHarness(t)
		seen := "etag-seen"
		h.orch.state.SetMetadataETag(&seen)
		h.purged.Store(true)
		// Under the actor lock as in the invoke path, so the deactivation
		// this branch queues is ordered after the turn.
		unlock, err := h.orch.lock.ContextLock(t.Context())
		require.NoError(t, err)
		completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-1"})
		unlock()
		require.ErrorIs(t, err, api.ErrInstanceNotFound)
		assert.Equal(t, todo.RunCompletedFalse, completed)
		assert.Equal(t, []string{"save+notify"}, h.snapshot(), "the parent must not learn of a completion whose state is gone")
	})

	t.Run("a new instance whose row is not readable yet is not a purge", func(t *testing.T) {
		t.Parallel()
		h := newCompletingHarness(t)
		h.purged.Store(true)
		completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-1"})
		require.NoError(t, err)
		assert.Equal(t, todo.RunCompletedTrue, completed)
		assert.Equal(t, []string{"save+notify", "call:" + todo.AddWorkflowEventMethod, "save-notify"}, h.snapshot(),
			"a store without read-your-writes must not fail a create that persisted")
	})

	t.Run("failed notify keeps the marker and arms the retry reminder", func(t *testing.T) {
		t.Parallel()
		h := newCompletingHarness(t)
		h.callErr = errors.New("parent unavailable")
		completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-1"})
		require.Error(t, err)
		assert.True(t, wferrors.IsRecoverable(err))
		assert.Equal(t, todo.RunCompletedFalse, completed)
		assert.Equal(t, []string{"save+notify", "call:" + todo.AddWorkflowEventMethod, "create:" + reminderNameParentNotify}, h.snapshot())
		require.NotNil(t, h.orch.state)
		assert.True(t, h.orch.state.ParentNotifyPending)
	})
}

func Test_runWorkflow_emptyInboxTerminalResendsPendingNotification(t *testing.T) {
	t.Parallel()

	for _, status := range []protos.OrchestrationStatus{
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
		protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED,
	} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()
			history := []*backend.HistoryEvent{
				{
					EventId: -1, Timestamp: timestamppb.Now(),
					EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
				},
				notifyStartEvent(), notifyCompletedEvent(status),
			}
			h := newNotifyHarness(t, history, nil, true, nil)
			h.orch.state = nil
			h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)

			completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: reminderNameParentNotify})
			require.NoError(t, err)
			assert.Equal(t, todo.RunCompletedTrue, completed)
			assert.Equal(t, []string{"call:" + todo.AddWorkflowEventMethod, "save-notify"}, h.snapshot())

			require.Len(t, h.calls, 1)
			var evt backend.HistoryEvent
			require.NoError(t, proto.Unmarshal(h.calls[0].GetMessage().GetData().GetValue(), &evt))
			assert.Equal(t, "testapp", evt.GetRouter().GetTargetAppID())
			if status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED {
				require.NotNil(t, evt.GetChildWorkflowInstanceCompleted())
				assert.Equal(t, notifyTaskID, evt.GetChildWorkflowInstanceCompleted().GetTaskScheduledId())
				assert.Equal(t, `"out"`, evt.GetChildWorkflowInstanceCompleted().GetResult().GetValue())
			} else {
				require.NotNil(t, evt.GetChildWorkflowInstanceFailed())
				assert.Equal(t, notifyTaskID, evt.GetChildWorkflowInstanceFailed().GetTaskScheduledId())
				assert.Equal(t, "boom", evt.GetChildWorkflowInstanceFailed().GetFailureDetails().GetErrorMessage())
			}
		})
	}
}

func Test_parentNotification_noParentOrCompletion(t *testing.T) {
	t.Parallel()

	h := newNotifyHarness(t, nil, nil, false, nil)
	st := wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "wf", ActivityActorType: "act"})
	start := notifyStartEvent()
	start.GetExecutionStarted().ParentInstance = nil
	st.AddToHistory(start)
	st.AddToHistory(notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED))
	msg, err := h.orch.parentNotification(t.Context(), st)
	require.NoError(t, err)
	assert.Nil(t, msg, "no parent, nothing to notify")

	st = wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "wf", ActivityActorType: "act"})
	st.AddToHistory(notifyStartEvent())
	msg, err = h.orch.parentNotification(t.Context(), st)
	require.NoError(t, err)
	assert.Nil(t, msg, "not completed, nothing to notify")
}

func Test_runWorkflow_strayFireResendsAndClears(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		},
		notifyStartEvent(), notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED),
	}
	h := newNotifyHarness(t, history, nil, true, nil)
	h.orch.state = nil
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)

	completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-stray"})
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.Equal(t, []string{"call:" + todo.AddWorkflowEventMethod, "save-notify"}, h.snapshot(), "a stray fire re-sends under the lock and clears the marker")

	h.ops = nil
	completed, err = h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-stray"})
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.Empty(t, h.snapshot(), "once delivered, later fires do not re-send")
}

func Test_addWorkflowEvent_dropsStaleGenerationChildCompletion(t *testing.T) {
	t.Parallel()

	created := &backend.HistoryEvent{
		EventId: 0, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
			ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{InstanceId: "child-gen2", Name: "child"},
		},
	}
	history := []*backend.HistoryEvent{
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "parent", WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID, ExecutionId: wrapperspb.String("exec-cur")},
		}}},
		created,
	}
	completion := func() *backend.HistoryEvent {
		return &backend.HistoryEvent{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{TaskScheduledId: 0}},
		}
	}

	t.Run("previous generation's child is acked without effect", func(t *testing.T) {
		t.Parallel()
		h := newNotifyHarness(t, history, nil, false, nil)
		require.NoError(t, h.orch.addWorkflowEvent(t.Context(), completion(), completionSender{instanceID: "child-gen1"}))
		assert.Empty(t, h.snapshot(), "no inbox save, no wake")
	})

	t.Run("same child id from a previous parent execution is acked without effect", func(t *testing.T) {
		t.Parallel()
		h := newNotifyHarness(t, history, nil, false, nil)
		require.NoError(t, h.orch.addWorkflowEvent(t.Context(), completion(), completionSender{instanceID: "child-gen2", parentExecutionID: "exec-old"}))
		assert.Empty(t, h.snapshot(), "no inbox save, no wake")
	})

	t.Run("current child and legacy senders are accepted", func(t *testing.T) {
		t.Parallel()
		for _, sender := range []completionSender{{instanceID: "child-gen2", parentExecutionID: "exec-cur"}, {instanceID: "child-gen2"}, {}} {
			h := newNotifyHarness(t, history, nil, false, nil)
			require.NoError(t, h.orch.addWorkflowEvent(t.Context(), completion(), sender))
			assert.Contains(t, h.snapshot(), "save", "sender %+v must reach the inbox", sender)
		}
	})
}

func Test_addWorkflowEvent_dropsChildCompletionForCompletedParent(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "parent", WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID, ExecutionId: wrapperspb.String("exec-cur")},
		}}},
		{EventId: 0, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
			ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{InstanceId: "child", Name: "child"},
		}},
		notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED),
	}
	failed := &backend.HistoryEvent{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 0}},
	}

	for _, sender := range []completionSender{{instanceID: "child", parentExecutionID: "exec-cur"}, {}} {
		h := newNotifyHarness(t, history, nil, false, nil)
		require.NoError(t, h.orch.addWorkflowEvent(t.Context(), failed, sender))
		assert.Empty(t, h.snapshot(), "sender %+v: a completed parent acks without an inbox save or wake", sender)
	}
}

func Test_runWorkflow_retryReminderResendDoesNotRearm(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		},
		notifyStartEvent(), notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED),
	}
	h := newNotifyHarness(t, history, nil, true, nil)
	h.orch.state = nil
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)
	h.callErr = errors.New("parent unavailable")

	completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: reminderNameParentNotify})
	require.Error(t, err)
	assert.True(t, wferrors.IsRecoverable(err))
	assert.Equal(t, todo.RunCompletedFalse, completed)
	assert.Equal(t, []string{"call:" + todo.AddWorkflowEventMethod}, h.snapshot(), "the nack retries under the reminder's failure policy; no due-now re-arm")
}

func Test_runWorkflow_nonChildEventOnCompletedChildResendsPending(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}},
		},
		notifyStartEvent(), notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED),
	}
	inbox := []*backend.HistoryEvent{{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{EventRaised: &protos.EventRaisedEvent{Name: "late"}},
	}}
	// The engine drops the work item of a completed instance; the turn
	// still runs its terminal block.
	scheduler := func(_ context.Context, wi *backend.WorkflowWorkItem) error {
		wi.Properties[todo.CallbackChannelProperty].(chan bool) <- true
		return nil
	}
	h := newNotifyHarness(t, history, inbox, true, scheduler)
	h.orch.state = nil
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(notifyChildID, nil, history)

	completed, err := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-er-9"})
	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.Equal(t, []string{"save", "call:" + todo.AddWorkflowEventMethod, "save-notify"}, h.snapshot(),
		"a late event on a completed child must not strand its pending notification")
}

func Test_loadInternalState_dropsOrphanParentNotifyRow(t *testing.T) {
	t.Parallel()

	rootStart := &backend.HistoryEvent{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "root", WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID},
		}},
	}
	history := []*backend.HistoryEvent{
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		rootStart,
	}
	h := newNotifyHarness(t, history, nil, true, nil)
	h.orch.state = nil

	state, _, err := h.orch.loadInternalState(t.Context())
	require.NoError(t, err)
	assert.False(t, state.ParentNotifyPending, "a parentless instance owes nothing")
	require.NoError(t, h.orch.signAndSaveState(t.Context(), state))
	assert.Equal(t, []string{"save-notify"}, h.snapshot(), "the orphan row goes with the next save")
}

func Test_addWorkflowEvent_staleCacheDoesNotAckChildCompletion(t *testing.T) {
	t.Parallel()

	history := []*backend.HistoryEvent{
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_WorkflowStarted{WorkflowStarted: &protos.WorkflowStartedEvent{}}},
		{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{
			Name: "parent", WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyChildID, ExecutionId: wrapperspb.String("exec-cur")},
		}}},
		{EventId: 0, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ChildWorkflowInstanceCreated{
			ChildWorkflowInstanceCreated: &protos.ChildWorkflowInstanceCreatedEvent{InstanceId: "child", Name: "child"},
		}},
		notifyCompletedEvent(protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED),
	}
	failed := &backend.HistoryEvent{
		EventId: -1, Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 0}},
	}
	h := newNotifyHarness(t, history, nil, false, nil)
	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), failed, completionSender{instanceID: "child"}), "a current cache acks the drop")
	require.NotNil(t, h.orch.state)

	// A peer wrote since: the metadata etag moved on under this cache.
	h.staleETag.Store(true)
	err := h.orch.addWorkflowEvent(t.Context(), failed, completionSender{instanceID: "child"})
	require.Error(t, err)
	assert.True(t, wferrors.IsRecoverable(err), "the sender must retry against a fresh load, not be acked off stale state")
	assert.Nil(t, h.orch.state, "the stale cache is dropped")
}

func Test_attestationInput(t *testing.T) {
	t.Parallel()

	started := &protos.ExecutionStartedEvent{Input: wrapperspb.String(`"gen2"`)}
	state := wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "dapr.internal.default.testapp.workflow", ActivityActorType: "dapr.internal.default.testapp.activity"})
	assert.Equal(t, `"gen2"`, attestationInput(state, started).GetValue(), "no ContinueAsNew: the start input")

	parent := &protos.ParentInstanceInfo{WorkflowInstance: &protos.WorkflowInstance{InstanceId: notifyParentID}}
	first := &protos.ExecutionStartedEvent{Input: wrapperspb.String(`"original"`), ParentInstance: parent}
	state.AddToHistory(&backend.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: first}})
	state.ApplyRuntimeStateChanges(&backend.WorkflowRuntimeState{ContinuedAsNew: true})
	assert.Equal(t, `"original"`, attestationInput(state, started).GetValue(), "after ContinueAsNew: the input the parent created the child with")

	empty := wfenginestate.NewState(wfenginestate.Options{AppID: "testapp", WorkflowActorType: "dapr.internal.default.testapp.workflow", ActivityActorType: "dapr.internal.default.testapp.activity"})
	empty.AddToHistory(&backend.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: &protos.ExecutionStartedEvent{ParentInstance: parent}}})
	empty.ApplyRuntimeStateChanges(&backend.WorkflowRuntimeState{ContinuedAsNew: true})
	assert.Empty(t, attestationInput(empty, started).GetValue(), "a child created without input attests an empty input, not the continued one")
}
