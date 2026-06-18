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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/dapr/pkg/actors/router"
	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	"github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

const dispatchRetryTestInstanceID = "dispatch-retry-test-wf"

func executionStartedEvent(instanceID string) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name: "TestWorkflow",
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}
}

func taskScheduledEvent(id int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   id,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "TestActivity"},
		},
	}
}

func remoteTaskScheduledEvent(id int32, targetAppID string) *backend.HistoryEvent {
	e := taskScheduledEvent(id)
	e.Router = &protos.TaskRouter{
		SourceAppID: "testapp",
		TargetAppID: &targetAppID,
	}
	return e
}

func taskCompletedEvent(taskScheduledID int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{TaskScheduledId: taskScheduledID},
		},
	}
}

func taskFailedEvent(taskScheduledID int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskFailed{
			TaskFailed: &protos.TaskFailedEvent{TaskScheduledId: taskScheduledID},
		},
	}
}

func newDispatchRetryTestState(history, inbox []*backend.HistoryEvent) *wfenginestate.State {
	s := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
	})
	for _, e := range history {
		s.AddToHistory(e)
	}
	for _, e := range inbox {
		s.AddToInbox(e)
	}
	return s
}

// dispatchRetryHarness wires an orchestrator against fake router, reminders,
// and state, capturing the side effects the dispatch-retry logic produces.
type dispatchRetryHarness struct {
	o                *orchestrator
	createdReminders []*actorapi.CreateReminderRequest
	deletedReminders []*actorapi.DeleteReminderRequest
	routerCalls      []*internalsv1pb.InternalInvokeRequest
}

// newDispatchRetryHarness builds the orchestrator harness. routerErr, when
// non-nil, is returned by every fake router Call (i.e. every activity
// dispatch fails with it). stateFake, when non-nil, replaces the default
// fake actor state store.
func newDispatchRetryHarness(t *testing.T, routerErr error, stateFake *statefake.Fake) *dispatchRetryHarness {
	t.Helper()

	h := &dispatchRetryHarness{}

	routerF := routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
		h.routerCalls = append(h.routerCalls, req)
		if routerErr != nil {
			return nil, routerErr
		}
		return &internalsv1pb.InternalInvokeResponse{}, nil
	})

	remindersF := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			h.createdReminders = append(h.createdReminders, req)
			return nil
		}).
		WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			h.deletedReminders = append(h.deletedReminders, req)
			return nil
		})

	if stateFake == nil {
		stateFake = statefake.New().WithGetFn(func(context.Context, *actorapi.GetStateRequest, bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{}, nil
		})
	}

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		WorkflowActorType: "workflow",
		ActivityActorType: "activity",
		Scheduler:         func(context.Context, *backend.WorkflowWorkItem) error { return nil },
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors: fake.New().
			WithRouter(func(context.Context) (router.Interface, error) { return routerF, nil }).
			WithReminders(func(context.Context) (reminders.Interface, error) { return remindersF, nil }).
			WithState(func(context.Context) (state.Interface, error) { return stateFake, nil }),
	})
	require.NoError(t, err)

	h.o = fact.GetOrCreate(dispatchRetryTestInstanceID).(*orchestrator)
	return h
}

// setCachedState primes the orchestrator's in-memory cache so
// loadInternalState returns the given state without touching the store.
func (h *dispatchRetryHarness) setCachedState(s *wfenginestate.State) {
	h.o.state = s
	h.o.rstate = runtimestate.NewWorkflowRuntimeState(dispatchRetryTestInstanceID, nil, s.History)
	h.o.ometa = h.o.ometaFromState(h.o.rstate, h.o.getExecutionStartedEvent(s))
}

func (h *dispatchRetryHarness) deletedReminderNames() []string {
	names := make([]string, 0, len(h.deletedReminders))
	for _, req := range h.deletedReminders {
		names = append(names, req.Name)
	}
	return names
}

func Test_unresolvedLocalTasks(t *testing.T) {
	start := executionStartedEvent(dispatchRetryTestInstanceID)

	tests := map[string]struct {
		history []*backend.HistoryEvent
		inbox   []*backend.HistoryEvent
		wantIDs []int32
	}{
		"no tasks scheduled": {
			history: []*backend.HistoryEvent{start},
			wantIDs: nil,
		},
		"unresolved local task is returned": {
			history: []*backend.HistoryEvent{start, taskScheduledEvent(2)},
			wantIDs: []int32{2},
		},
		"task completed in history is resolved": {
			history: []*backend.HistoryEvent{start, taskScheduledEvent(2), taskCompletedEvent(2)},
			wantIDs: nil,
		},
		"task failed in history is resolved": {
			history: []*backend.HistoryEvent{start, taskScheduledEvent(2), taskFailedEvent(2)},
			wantIDs: nil,
		},
		"task completed in inbox is resolved": {
			history: []*backend.HistoryEvent{start, taskScheduledEvent(2)},
			inbox:   []*backend.HistoryEvent{taskCompletedEvent(2)},
			wantIDs: nil,
		},
		"cross-app task is excluded": {
			history: []*backend.HistoryEvent{start, remoteTaskScheduledEvent(2, "otherapp")},
			wantIDs: nil,
		},
		"mixed tasks preserve history order": {
			history: []*backend.HistoryEvent{
				start,
				taskScheduledEvent(2),
				taskScheduledEvent(3),
				remoteTaskScheduledEvent(4, "otherapp"),
				taskScheduledEvent(5),
				taskCompletedEvent(3),
			},
			wantIDs: []int32{2, 5},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := newDispatchRetryTestState(tc.history, tc.inbox)

			unresolved := unresolvedLocalTasks(s)

			gotIDs := make([]int32, 0, len(unresolved))
			for _, e := range unresolved {
				gotIDs = append(gotIDs, e.GetEventId())
			}
			if tc.wantIDs == nil {
				assert.Empty(t, gotIDs)
			} else {
				assert.Equal(t, tc.wantIDs, gotIDs)
			}
		})
	}
}

func Test_redispatchStranded_deletesReminderWhenNothingUnresolved(t *testing.T) {
	h := newDispatchRetryHarness(t, nil, nil)
	h.setCachedState(newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
		taskCompletedEvent(2),
	}, nil))

	err := h.o.redispatchStranded(t.Context(), nil)

	require.NoError(t, err)
	assert.Empty(t, h.routerCalls, "no activity should be re-dispatched")
	assert.Equal(t, []string{reminderNameDispatchRetry}, h.deletedReminderNames())
}

func Test_redispatchStranded_skipsCrossAppTasks(t *testing.T) {
	h := newDispatchRetryHarness(t, nil, nil)
	h.setCachedState(newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		remoteTaskScheduledEvent(2, "otherapp"),
	}, nil))

	err := h.o.redispatchStranded(t.Context(), nil)

	require.NoError(t, err)
	assert.Empty(t, h.routerCalls, "cross-app tasks must not be re-dispatched without propagated history")
	assert.Equal(t, []string{reminderNameDispatchRetry}, h.deletedReminderNames())
}

func Test_redispatchStranded_redispatchesUnresolvedLocalTask(t *testing.T) {
	h := newDispatchRetryHarness(t, nil, nil)
	h.setCachedState(newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
	}, nil))

	err := h.o.redispatchStranded(t.Context(), nil)

	require.NoError(t, err)
	require.Len(t, h.routerCalls, 1)
	assert.Equal(t, todo.ExecuteActivityMethod, h.routerCalls[0].GetMessage().GetMethod())
	assert.Equal(t, []string{reminderNameDispatchRetry}, h.deletedReminderNames())
}

func Test_redispatchStranded_toleratesDuplicateInvocation(t *testing.T) {
	h := newDispatchRetryHarness(t, todo.ErrDuplicateInvocation, nil)
	h.setCachedState(newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
	}, nil))

	err := h.o.redispatchStranded(t.Context(), nil)

	require.NoError(t, err, "an in-flight activity (duplicate invocation) is not a failure")
	require.Len(t, h.routerCalls, 1)
	assert.Equal(t, []string{reminderNameDispatchRetry}, h.deletedReminderNames())
}

func Test_redispatchStranded_keepsReminderOnDispatchFailure(t *testing.T) {
	h := newDispatchRetryHarness(t, errors.New("activity actor unavailable"), nil)
	h.setCachedState(newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
	}, nil))

	err := h.o.redispatchStranded(t.Context(), nil)

	require.Error(t, err)
	assert.True(t, wferrors.IsRecoverable(err), "dispatch failure must be recoverable so the reminder fires again")
	assert.Empty(t, h.deletedReminders, "the reminder must stay in place while dispatch keeps failing")
}

func Test_redispatchStranded_deletesReminderWhenStatePurged(t *testing.T) {
	// No cached state and an empty fake store: loadInternalState returns a
	// nil state, signaling the workflow has been purged.
	h := newDispatchRetryHarness(t, nil, nil)

	err := h.o.redispatchStranded(t.Context(), nil)

	require.NoError(t, err)
	assert.Empty(t, h.routerCalls)
	assert.Equal(t, []string{reminderNameDispatchRetry}, h.deletedReminderNames())
}

// storeFromState renders a saved workflow state into a fake actor state
// store, so runWorkflow's empty-inbox reload path can read it back.
func storeFromState(t *testing.T, s *wfenginestate.State) *statefake.Fake {
	t.Helper()

	req, err := s.GetSaveRequest(dispatchRetryTestInstanceID)
	require.NoError(t, err)

	kv := make(map[string][]byte)
	for _, op := range req.Operations {
		if up, ok := op.Request.(actorapi.TransactionalUpsert); ok {
			switch v := up.Value.(type) {
			case []byte:
				kv[up.Key] = v
			case string:
				kv[up.Key] = []byte(v)
			default:
				t.Fatalf("unexpected upsert value type %T for key %s", up.Value, up.Key)
			}
		}
	}

	return statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{Data: kv[req.Key]}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			res := make(actorapi.BulkStateResponse, len(req.Keys))
			for _, k := range req.Keys {
				res[k] = actorapi.BulkStateEntry{Data: kv[k]}
			}
			return res, nil
		})
}

func Test_runWorkflow_emptyInboxRecreatesDispatchRetryReminder(t *testing.T) {
	// A running workflow with an empty inbox and a durable TaskScheduled
	// whose result never arrived: the empty-inbox path must recreate the
	// dispatch-retry safety reminder, serialized on the workflow name.
	persisted := newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
	}, nil)
	h := newDispatchRetryHarness(t, nil, storeFromState(t, persisted))

	completed, err := h.o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-recovery-test"})

	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)

	require.Len(t, h.createdReminders, 1)
	created := h.createdReminders[0]
	assert.Equal(t, reminderNameDispatchRetry, created.Name)
	assert.Equal(t, dispatchRetryTestInstanceID, created.ActorID)
	require.NotNil(t, created.ConcurrencyKey, "dispatch-retry must carry the workflow-name concurrency key")
	assert.Equal(t, "TestWorkflow", *created.ConcurrencyKey)
}

func Test_runWorkflow_emptyInboxNoRecreateWhenTasksResolved(t *testing.T) {
	// Same shape, but the task already has its completion in history: the
	// empty-inbox path must stay a no-op and not create any reminder.
	persisted := newDispatchRetryTestState([]*backend.HistoryEvent{
		executionStartedEvent(dispatchRetryTestInstanceID),
		taskScheduledEvent(2),
		taskCompletedEvent(2),
	}, nil)
	h := newDispatchRetryHarness(t, nil, storeFromState(t, persisted))

	completed, err := h.o.runWorkflow(t.Context(), &actorapi.Reminder{Name: "new-event-recovery-test"})

	require.NoError(t, err)
	assert.Equal(t, todo.RunCompletedTrue, completed)
	assert.Empty(t, h.createdReminders)
}
