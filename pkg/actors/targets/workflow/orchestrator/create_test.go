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
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/actors/reminders"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	actorstate "github.com/dapr/dapr/pkg/actors/state"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// createHarness captures every state save and reminder create in a single
// ordered operation log so tests can assert save-before-create ordering.
type createHarness struct {
	lock    sync.Mutex
	ops     []string
	creates []*actorapi.CreateReminderRequest
	saved   bool

	createErr error

	// armedReminder, when non-nil, is returned by the reminders Get fake:
	// the pending start's scheduler reminder exists. Nil means missing.
	armedReminder *actorapi.Reminder

	orch *orchestrator
}

func newCreateHarness(t *testing.T, instanceID string) *createHarness {
	t.Helper()

	h := new(createHarness)

	fakeRems := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			if h.createErr != nil {
				return h.createErr
			}
			h.ops = append(h.ops, "create:"+req.Name)
			h.creates = append(h.creates, req)
			return nil
		}).
		WithGet(func(context.Context, *actorapi.GetReminderRequest) (*actorapi.Reminder, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			return h.armedReminder, nil
		})

	fakeState := statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			// No stored state until the first save: fresh instance. After a
			// save the metadata row exists, as the post-save etag refresh
			// expects.
			if h.saved && req.Key == wfenginestate.MetadataKey {
				etag := "etag"
				return &actorapi.StateResponse{Data: []byte{1}, ETag: &etag}, nil
			}
			return &actorapi.StateResponse{}, nil
		}).
		WithTransactionalStateOperationFn(func(context.Context, bool, *actorapi.TransactionalRequest, bool) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			h.ops = append(h.ops, "save")
			h.saved = true
			return nil
		})

	actors := fake.New().
		WithReminders(func(context.Context) (reminders.Interface, error) {
			return fakeRems, nil
		}).
		WithState(func(context.Context) (actorstate.Interface, error) {
			return fakeState, nil
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
	})
	require.NoError(t, err)

	h.orch = fact.GetOrCreate(instanceID).(*orchestrator)

	return h
}

func startEventFor(instanceID string, ts time.Time, mutate func(*protos.ExecutionStartedEvent)) *backend.HistoryEvent {
	es := &protos.ExecutionStartedEvent{
		Name:  "TestWorkflow",
		Input: wrapperspb.String(`"in"`),
		WorkflowInstance: &protos.WorkflowInstance{
			InstanceId: instanceID,
		},
	}
	if mutate != nil {
		mutate(es)
	}
	return &backend.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.New(ts),
		EventType: &protos.HistoryEvent_ExecutionStarted{ExecutionStarted: es},
	}
}

// primePendingStart primes the orchestrator with a saved-but-never-run state:
// empty history, the given events in the inbox.
func (h *createHarness) primePendingStart(savedStart *backend.HistoryEvent, extraInbox ...*backend.HistoryEvent) *wfenginestate.State {
	state := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	state.AddToInbox(savedStart)
	for _, e := range extraInbox {
		state.AddToInbox(e)
	}

	h.orch.state = state
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(h.orch.actorID, nil, nil)
	h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, savedStart.GetExecutionStarted())
	return state
}

func createRequestBytes(t *testing.T, startEvent *backend.HistoryEvent) []byte {
	t.Helper()
	b, err := proto.Marshal(&backend.CreateWorkflowInstanceRequest{StartEvent: startEvent})
	require.NoError(t, err)
	return b
}

func Test_scheduleWorkflowStart_savesBeforeReminderCreate(t *testing.T) {
	const instanceID = "test-start-order"

	ts := time.Now()
	h := newCreateHarness(t, instanceID)

	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, startEventFor(instanceID, ts, nil))))

	h.lock.Lock()
	defer h.lock.Unlock()
	wantName := "start-es-" + strconv.Itoa(int(ts.UnixNano()))
	require.Equal(t, []string{"save", "create:" + wantName}, h.ops,
		"the inbox must be durably saved before the start reminder is created")

	require.Len(t, h.creates, 1)
	got := h.creates[0]
	assert.Equal(t, "dapr.internal.default.testapp.workflow", got.ActorType)
	assert.Equal(t, instanceID, got.ActorID)
	assert.Equal(t, ts.UTC().Format(time.RFC3339Nano), got.DueTime)

	require.Len(t, h.orch.state.Inbox, 1)
	assert.NotNil(t, h.orch.state.Inbox[0].GetExecutionStarted())
}

func Test_scheduleWorkflowStart_honorsScheduledStartTimestamp(t *testing.T) {
	const instanceID = "test-start-delayed"

	ts := time.Now()
	delayed := ts.Add(time.Hour)
	h := newCreateHarness(t, instanceID)

	start := startEventFor(instanceID, ts, func(es *protos.ExecutionStartedEvent) {
		es.ScheduledStartTimestamp = timestamppb.New(delayed)
	})
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, start)))

	h.lock.Lock()
	defer h.lock.Unlock()
	require.Len(t, h.creates, 1)
	assert.Equal(t, delayed.UTC().Format(time.RFC3339Nano), h.creates[0].DueTime,
		"delayed starts must keep the future due time")
}

func Test_scheduleWorkflowStart_reminderCreateFailureReturnsErrorAfterSave(t *testing.T) {
	const instanceID = "test-start-create-fails"

	h := newCreateHarness(t, instanceID)
	h.createErr = errors.New("scheduler exploded")

	err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, startEventFor(instanceID, time.Now(), nil)))
	require.ErrorContains(t, err, "scheduler exploded")

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Equal(t, []string{"save"}, h.ops,
		"the save must have happened; the create failure surfaces to the caller for retry")
	require.Len(t, h.orch.state.Inbox, 1)
	assert.NotNil(t, h.orch.state.Inbox[0].GetExecutionStarted(),
		"the ExecutionStarted inbox row is durable, recoverable via the pending-start path")
}

func Test_createWorkflowInstance_pendingStartReassertsByDeterministicName(t *testing.T) {
	const instanceID = "test-pending-reassert"

	savedTS := time.Now().Add(-time.Minute)
	h := newCreateHarness(t, instanceID)
	h.primePendingStart(startEventFor(instanceID, savedTS, nil))

	// A client retry of the same logical create regenerates timestamp (and
	// ExecutionId); the re-assert must derive the name from the SAVED event.
	incoming := startEventFor(instanceID, time.Now(), nil)
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	wantName := "start-es-" + strconv.Itoa(int(savedTS.UnixNano()))
	require.Equal(t, []string{"create:" + wantName}, h.ops,
		"pending-start re-drive must re-assert exactly one reminder named from the saved event, with no state save")
	assert.Len(t, h.orch.state.Inbox, 1, "no duplicate inbox append")
}

func Test_createWorkflowInstance_pendingStartWithArmedReminderAlreadyExists(t *testing.T) {
	const instanceID = "test-pending-armed"

	// The pending start's scheduler reminder exists: this is a healthy
	// concurrent duplicate create of an identical workflow (the reuse race),
	// NOT a stranded start. The duplicate must keep failing with
	// AlreadyExists and must not re-assert anything.
	h := newCreateHarness(t, instanceID)
	h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), nil))
	h.armedReminder = &actorapi.Reminder{Name: "start-es-1"}

	incoming := startEventFor(instanceID, time.Now(), nil)
	err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops, "an armed pending start must not be re-driven or saved")
}

func Test_createWorkflowInstance_pendingStartParentWithArmedReminderNoop(t *testing.T) {
	const instanceID = "test-pending-parent-armed"

	parent := func(es *protos.ExecutionStartedEvent) {
		es.ParentInstance = &protos.ParentInstanceInfo{
			TaskScheduledId:  3,
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
	}

	h := newCreateHarness(t, instanceID)
	h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parent))
	h.armedReminder = &actorapi.Reminder{Name: "start-es-1"}

	// The duplicate child creation is ignored as before; with the reminder
	// armed there is nothing to re-drive.
	incoming := startEventFor(instanceID, time.Now(), parent)
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
}

func Test_createWorkflowInstance_pendingStartMismatchAlreadyExists(t *testing.T) {
	const instanceID = "test-pending-mismatch"

	for name, mutate := range map[string]func(*protos.ExecutionStartedEvent){
		"different name":  func(es *protos.ExecutionStartedEvent) { es.Name = "OtherWorkflow" },
		"different input": func(es *protos.ExecutionStartedEvent) { es.Input = wrapperspb.String(`"other"`) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newCreateHarness(t, instanceID)
			h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), nil))

			incoming := startEventFor(instanceID, time.Now(), mutate)
			err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming))
			require.Error(t, err)
			assert.Equal(t, codes.AlreadyExists, status.Code(err))

			h.lock.Lock()
			defer h.lock.Unlock()
			assert.Empty(t, h.ops, "a conflicting create must not re-assert or save anything")
		})
	}
}

func Test_createWorkflowInstance_pendingStartSameParentReasserts(t *testing.T) {
	const instanceID = "test-pending-parent"

	parent := func(es *protos.ExecutionStartedEvent) {
		es.ParentInstance = &protos.ParentInstanceInfo{
			TaskScheduledId:  3,
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
	}

	savedTS := time.Now().Add(-time.Minute)
	h := newCreateHarness(t, instanceID)
	h.primePendingStart(startEventFor(instanceID, savedTS, parent))

	// The parent re-executes and re-issues the child creation with a fresh
	// timestamp: the pending-start child must be re-driven, not ignored.
	incoming := startEventFor(instanceID, time.Now(), parent)
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	wantName := "start-es-" + strconv.Itoa(int(savedTS.UnixNano()))
	require.Equal(t, []string{"create:" + wantName}, h.ops)
}

// parentWithExec returns a startEventFor mutate hook attaching a
// ParentInstanceInfo whose WorkflowInstance carries the given ExecutionId, or
// none when execID is empty.
func parentWithExec(execID string) func(*protos.ExecutionStartedEvent) {
	return func(es *protos.ExecutionStartedEvent) {
		pi := &protos.ParentInstanceInfo{
			TaskScheduledId:  3,
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
		if execID != "" {
			pi.WorkflowInstance.ExecutionId = wrapperspb.String(execID)
		}
		es.ParentInstance = pi
	}
}

func Test_createWorkflowInstance_sameParentSameExecutionDedups(t *testing.T) {
	const instanceID = "test-parent-same-exec"

	h := newCreateHarness(t, instanceID)
	h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec("exec-a")))
	h.armedReminder = &actorapi.Reminder{Name: "start-es-1"}

	// A crash-replay duplicate carries the same parent ExecutionId and is
	// ignored exactly as when no ExecutionId is present at all.
	incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-a"))
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	assert.Empty(t, h.ops)
}

func Test_createWorkflowInstance_parentExecutionMismatchAlreadyExists(t *testing.T) {
	const instanceID = "test-parent-exec-mismatch"

	// A differing parent ExecutionId means the parent continued-as-new (or was
	// recreated) and scheduled a genuinely new child colliding with a live
	// child of a previous execution. It must fail with AlreadyExists so the
	// parent's child task is faulted, never silently deduplicated. The
	// reminder-missing variant proves the pending-start re-drive gate does not
	// resurrect the OLD execution's start either.
	for name, armed := range map[string]*actorapi.Reminder{
		"reminder armed":   {Name: "start-es-1"},
		"reminder missing": nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newCreateHarness(t, instanceID)
			h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec("exec-a")))
			h.armedReminder = armed

			incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-b"))
			err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming))
			require.Error(t, err)
			assert.Equal(t, codes.AlreadyExists, status.Code(err))
			assert.Contains(t, err.Error(), "already exists")
			assert.Contains(t, err.Error(), "previous execution")

			h.lock.Lock()
			defer h.lock.Unlock()
			assert.Empty(t, h.ops, "a colliding create from a new parent execution must not save or re-arm the old execution's start")
		})
	}
}

func Test_createWorkflowInstance_parentExecutionNilEitherSideDedups(t *testing.T) {
	const instanceID = "test-parent-exec-nil"

	// A missing ExecutionId on either side (older persisted state, rerun-path
	// creations that omit it) must keep today's conservative dedup.
	for name, execs := range map[string]struct{ saved, incoming string }{
		"saved set, incoming nil": {saved: "exec-a", incoming: ""},
		"saved nil, incoming set": {saved: "", incoming: "exec-b"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCreateHarness(t, instanceID)
			h.primePendingStart(startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec(execs.saved)))
			h.armedReminder = &actorapi.Reminder{Name: "start-es-1"}

			incoming := startEventFor(instanceID, time.Now(), parentWithExec(execs.incoming))
			require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

			h.lock.Lock()
			defer h.lock.Unlock()
			assert.Empty(t, h.ops)
		})
	}
}

func Test_sameParentExecution(t *testing.T) {
	pi := func(execID string) *protos.ParentInstanceInfo {
		p := &protos.ParentInstanceInfo{
			WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent-1"},
		}
		if execID != "" {
			p.WorkflowInstance.ExecutionId = wrapperspb.String(execID)
		}
		return p
	}

	tests := map[string]struct {
		existing *protos.ParentInstanceInfo
		incoming *protos.ParentInstanceInfo
		want     bool
	}{
		"both nil":  {existing: pi(""), incoming: pi(""), want: true},
		"a nil":     {existing: pi(""), incoming: pi("x"), want: true},
		"b nil":     {existing: pi("x"), incoming: pi(""), want: true},
		"equal":     {existing: pi("x"), incoming: pi("x"), want: true},
		"different": {existing: pi("x"), incoming: pi("y"), want: false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, sameParentExecution(tc.existing, tc.incoming))
		})
	}
}

func Test_createWorkflowInstance_pendingStartWithRaisedEventStillReasserts(t *testing.T) {
	const instanceID = "test-pending-raised"

	savedTS := time.Now().Add(-time.Minute)
	h := newCreateHarness(t, instanceID)
	h.primePendingStart(
		startEventFor(instanceID, savedTS, nil),
		&backend.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{Name: "early"},
			},
		},
	)

	incoming := startEventFor(instanceID, time.Now(), nil)
	require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))

	h.lock.Lock()
	defer h.lock.Unlock()
	wantName := "start-es-" + strconv.Itoa(int(savedTS.UnixNano()))
	require.Equal(t, []string{"create:" + wantName}, h.ops,
		"a pre-start RaiseEvent in the inbox must not prevent the pending-start re-drive")
}

func Test_createWorkflowInstance_completedChildWithPendingNotification(t *testing.T) {
	const instanceID = "test-pending-child"

	prime := func(t *testing.T) *createHarness {
		h := newCreateHarness(t, instanceID)
		start := startEventFor(instanceID, time.Now().Add(-time.Minute), parentWithExec("exec-a"))
		state := wfenginestate.NewState(wfenginestate.Options{
			AppID:             "testapp",
			Namespace:         "default",
			WorkflowActorType: "dapr.internal.default.testapp.workflow",
			ActivityActorType: "dapr.internal.default.testapp.activity",
		})
		state.AddToHistory(start)
		state.AddToHistory(&backend.HistoryEvent{
			EventId: -1, Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
			}},
		})
		state.SetParentNotifyPending(true)
		h.orch.state = state
		h.orch.rstate = runtimestate.NewWorkflowRuntimeState(h.orch.actorID, nil, state.History)
		h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, start.GetExecutionStarted())
		return h
	}

	t.Run("the creating parent's replay is a no-op", func(t *testing.T) {
		h := prime(t)
		incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-a"))
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming)))
		h.lock.Lock()
		defer h.lock.Unlock()
		assert.Empty(t, h.ops, "no reset, no new start")
	})

	t.Run("any other creation is refused, retryably, until the parent acknowledged", func(t *testing.T) {
		h := prime(t)
		incoming := startEventFor(instanceID, time.Now(), parentWithExec("exec-b"))
		err := h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, incoming))
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
		h.lock.Lock()
		defer h.lock.Unlock()
		assert.Empty(t, h.ops)
	})
}
