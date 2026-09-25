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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/pendingstart"
	"github.com/dapr/dapr/pkg/config"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

// wakeHarness captures state saves, reminder creates/deletes and router
// CallReminder invocations in one ordered log. The wake runs on a detached
// goroutine, so assertions on wake effects must be eventual.
type wakeHarness struct {
	saved bool
	lock  sync.Mutex
	ops   []string

	callReminderErr error
	deleteErr       error
	createErrFor    map[string]error
	dues            map[string]string // reminder name -> DueTime of its last create
	reminderGate    chan struct{}     // when non-nil, CallReminder blocks on it (or ctx)

	attempts atomic.Int64 // CallReminder invocations, successful or not
	calls    []*actorapi.Reminder

	fact *factory
	orch *orchestrator
}

func (h *wakeHarness) snapshotOps() []string {
	h.lock.Lock()
	defer h.lock.Unlock()
	return append([]string(nil), h.ops...)
}

func newWakeHarness(t *testing.T, instanceID string, fastPath bool) *wakeHarness {
	t.Helper()

	h := new(wakeHarness)

	fakeRems := remindersfake.New().
		WithCreate(func(_ context.Context, req *actorapi.CreateReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			if err, ok := h.createErrFor[req.Name]; ok {
				return err
			}
			h.ops = append(h.ops, "create:"+req.Name)
			if h.dues == nil {
				h.dues = map[string]string{}
			}
			h.dues[req.Name] = req.DueTime
			return nil
		}).
		WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			h.lock.Lock()
			defer h.lock.Unlock()
			if h.deleteErr != nil {
				return h.deleteErr
			}
			h.ops = append(h.ops, "delete:"+req.Name)
			return nil
		})

	fakeState := statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			h.lock.Lock()
			defer h.lock.Unlock()
			// The metadata row exists once something was saved, as the
			// post-save etag refresh expects.
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

	fakeRouter := routerfake.New().WithCallReminderFn(func(ctx context.Context, rem *actorapi.Reminder) error {
		h.attempts.Add(1)
		if h.reminderGate != nil {
			select {
			case <-h.reminderGate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		h.lock.Lock()
		defer h.lock.Unlock()
		if h.callReminderErr != nil {
			return h.callReminderErr
		}
		h.ops = append(h.ops, "callReminder:"+rem.Name)
		h.calls = append(h.calls, rem)
		return nil
	})

	actors := fake.New().
		WithReminders(func(context.Context) (actorreminders.Interface, error) {
			return fakeRems, nil
		}).
		WithState(func(context.Context) (actorstate.Interface, error) {
			return fakeState, nil
		}).
		WithRouter(func(context.Context) (router.Interface, error) {
			return fakeRouter, nil
		})

	fact, err := New(t.Context(), Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
		ActorTypeBuilder:  common.NewActorTypeBuilder("default"),
		Actors:            actors,
		FastPath:          fastPath,
	})
	require.NoError(t, err)

	h.fact = fact.(*factory)
	h.orch = fact.GetOrCreate(instanceID).(*orchestrator)

	return h
}

// primeRunning primes the orchestrator with a running workflow that has an
// outstanding TaskScheduled, so an incoming TaskCompleted takes the normal
// (non-dedup) AddWorkflowEvent path.
func (h *wakeHarness) primeRunning(t *testing.T, instanceID string, scheduled int32) {
	t.Helper()

	startEvent := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:  "TestWorkflow",
				Input: wrapperspb.String(`null`),
				WorkflowInstance: &protos.WorkflowInstance{
					InstanceId: instanceID,
				},
			},
		},
	}
	taskScheduled := &protos.HistoryEvent{
		EventId:   scheduled,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act"},
		},
	}
	history := []*backend.HistoryEvent{startEvent, taskScheduled}

	wfState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	for _, e := range history {
		wfState.AddToHistory(e)
	}

	h.orch.state = wfState
	h.orch.rstate = runtimestate.NewWorkflowRuntimeState(instanceID, nil, history)
	h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, startEvent.GetExecutionStarted())
}

func taskCompletedEvent(scheduled int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: scheduled,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}
}

func Test_localWake_firesAfterCreateAndDeletesBackstop(t *testing.T) {
	const instanceID = "test-wake-fires"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	// v2: the per-event reminder pair is elided entirely. The janitor is the
	// durable backstop, the turn is driven locally, and nothing is deleted.
	want := []string{"save", "create:new-event-janitor", "callReminder:new-event-tc-7"}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, want, h.snapshotOps())
	}, time.Second*5, time.Millisecond*5,
		"the local drive must fire strictly after save+janitor, with no per-event reminder and no delete")

	h.lock.Lock()
	defer h.lock.Unlock()
	require.Len(t, h.calls, 1)
	rem := h.calls[0]
	assert.Equal(t, "dapr.internal.default.testapp.workflow", rem.ActorType)
	assert.Equal(t, instanceID, rem.ActorID)
	assert.Nil(t, rem.Data, "wake-up reminders carry no payload")
	assert.False(t, rem.SkipLock, "workflow reminders keep the router lock semantics")
}

func Test_localWake_flagOffNoop(t *testing.T) {
	const instanceID = "test-wake-off"

	h := newWakeHarness(t, instanceID, false)
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, []string{"save", "create:new-event-tc-7"}, h.snapshotOps(),
		"with the feature off the wake path must not invoke or delete anything")
}

func Test_localWake_startPath(t *testing.T) {
	t.Run("immediate start fires the wake", func(t *testing.T) {
		const instanceID = "test-wake-start"

		h := newWakeHarness(t, instanceID, true)

		ts := time.Now()
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(),
			createRequestBytes(t, startEventFor(instanceID, ts, nil))))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			ops := h.snapshotOps()
			// The durable start reminder is the dormant backstop of the
			// local drive; it is elided once the first turn commits.
			if assert.Len(c, ops, 3) {
				assert.Equal(c, "save", ops[0])
				assert.Contains(c, ops[1], "create:start-es-")
				assert.Contains(c, ops[2], "callReminder:start-es-")
			}
		}, time.Second*5, time.Millisecond*5)
	})

	t.Run("delayed start does not fire the wake", func(t *testing.T) {
		const instanceID = "test-wake-delayed"

		h := newWakeHarness(t, instanceID, true)

		start := startEventFor(instanceID, time.Now(), func(es *protos.ExecutionStartedEvent) {
			es.ScheduledStartTimestamp = timestamppb.New(time.Now().Add(time.Hour))
		})
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(), createRequestBytes(t, start)))

		time.Sleep(time.Millisecond * 100)
		ops := h.snapshotOps()
		require.Len(t, ops, 2,
			"a delayed start must keep its scheduler due time: no local wake, no backstop delete")
		assert.Equal(t, "save", ops[0])
		assert.Contains(t, ops[1], "create:start-es-")
	})
}

func Test_localWake_failureLeavesJanitor(t *testing.T) {
	const instanceID = "test-wake-err"

	h := newWakeHarness(t, instanceID, true)
	// Compress the retry schedule so the failure resolves quickly.
	h.fact.driveRetryBackoffs = []time.Duration{time.Millisecond * 5, time.Millisecond * 5, time.Millisecond * 5}
	h.callReminderErr = errors.New("wake failed")
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	// A failed drive is retried in place, then handed to the janitor: no
	// durable per-event reminder is ever created and nothing is deleted.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(4), h.attempts.Load(), "the initial attempt plus every in-place retry must run")
		assert.False(c, h.orch.driveRunning.Load(), "the drive loop must wind down after the retries")
	}, time.Second*5, time.Millisecond*5)

	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, []string{"save", "create:new-event-janitor"}, h.snapshotOps(),
		"a failed drive must leave the janitor as the only driver")
}

func Test_localWake_closedActorStopsRetrying(t *testing.T) {
	const instanceID = "test-wake-closed"

	h := newWakeHarness(t, instanceID, true)
	h.fact.driveRetryBackoffs = []time.Duration{time.Millisecond * 5, time.Millisecond * 5, time.Millisecond * 5}
	h.callReminderErr = targeterrors.NewClosed("test")
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	// A closed actor is the migration/teardown path: the drive is lost, not
	// slow, so the loop exits without retrying; the janitor drives the turn
	// on the new owner.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, h.orch.driveRunning.Load(), "the drive loop must wind down")
	}, time.Second*5, time.Millisecond*5)

	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, int64(1), h.attempts.Load(), "a lost drive must not be retried in place")
	assert.Equal(t, []string{"save", "create:new-event-janitor"}, h.snapshotOps())
}

func Test_redispatchSuppressedSignals(t *testing.T) {
	const instanceID = "test-redispatch-signals"

	h := newWakeHarness(t, instanceID, true)
	o := h.orch

	assert.False(t, o.redispatchSuppressed(),
		"a fresh activation must never suppress the janitor re-dispatch")

	// A durable commit must NOT suppress the re-dispatch pass: sibling
	// activities or external events can keep committing forever while one
	// activity host is dead, so instance-wide progress proves nothing about
	// a given task.
	assert.False(t, o.redispatchSuppressed(),
		"instance progress must not gate task re-dispatch")

	// A running drive loop suppresses regardless of commit age.
	o.driveRunning.Store(true)
	assert.True(t, o.redispatchSuppressed())
	o.driveRunning.Store(false)
	assert.False(t, o.redispatchSuppressed())
}

func Test_localWake_janitorOncePerResidency(t *testing.T) {
	const instanceID = "test-wake-janitor-once"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)
	// Second outstanding task so the second completion passes dedup.
	h.orch.state.AddToHistory(&protos.HistoryEvent{
		EventId:   8,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act2"},
		},
	})

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))
	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(8), completionSender{}))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		janitors, wakes := 0, 0
		for _, op := range h.snapshotOps() {
			if op == "create:new-event-janitor" {
				janitors++
			}
			if strings.HasPrefix(op, "callReminder:") {
				wakes++
			}
		}
		assert.Equal(c, 1, janitors, "the janitor is asserted once per residency")
		// Concurrent drives coalesce (a drive drains the whole inbox), so
		// two rapid events may produce one or two wakes, never zero.
		assert.GreaterOrEqual(c, wakes, 1, "the events must be driven")
	}, time.Second*5, time.Millisecond*5)
}

func Test_localWake_janitorCreateFailureFallsBack(t *testing.T) {
	const instanceID = "test-wake-janitor-fail"

	h := newWakeHarness(t, instanceID, true)
	h.createErrFor = map[string]error{janitorReminderName: errors.New("scheduler down")}
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	// Durability first: without a janitor the durable per-event reminder is
	// created exactly as with the feature off (and the drive still fires).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"save", "create:new-event-tc-7", "callReminder:new-event-tc-7"}, h.snapshotOps())
	}, time.Second*5, time.Millisecond*5)
}

func Test_janitor_terminalSelfDeletes(t *testing.T) {
	const instanceID = "test-janitor-terminal"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)
	// Mark the runtime state completed: the janitor fire must self-delete.
	h.orch.rstate.CompletedEvent = &protos.ExecutionCompletedEvent{
		WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
	}

	require.NoError(t, h.orch.runJanitor(t.Context(), &actorapi.Reminder{Name: janitorReminderName}))
	assert.Contains(t, h.snapshotOps(), "delete:new-event-janitor")
}

func Test_localWake_deleteNotFoundTolerated(t *testing.T) {
	const instanceID = "test-wake-notfound"

	h := newWakeHarness(t, instanceID, true)
	h.deleteErr = status.Error(codes.NotFound, "no such reminder")
	h.primeRunning(t, instanceID, 7)

	// deleteJanitor must tolerate NotFound (never asserted this residency,
	// or already swept by an old binary's DeleteByActorID).
	h.orch.deleteJanitor(t.Context())

	require.NoError(t, h.fact.HaltAll(t.Context()))
}

func Test_localWake_haltAllDrainsGoroutines(t *testing.T) {
	const instanceID = "test-wake-halt"

	h := newWakeHarness(t, instanceID, true)
	h.reminderGate = make(chan struct{})
	h.primeRunning(t, instanceID, 7)

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), taskCompletedEvent(7), completionSender{}))

	// The wake goroutine is parked on the gate; HaltAll must cancel it and
	// return rather than deadlocking.
	done := make(chan error, 1)
	go func() { done <- h.fact.HaltAll(t.Context()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		t.Fatal("HaltAll did not drain the parked wake goroutine")
	}

	// The cancelled wake must neither delete anything nor create a durable
	// per-event reminder: the janitor drives the turn on the new owner.
	assert.False(t, h.orch.driveRunning.Load(), "HaltAll must drain the drive loop")
	assert.Equal(t, []string{"save", "create:new-event-janitor"}, h.snapshotOps())

	// The factory keeps serving after HaltAll (placement churn also calls
	// it): a fresh wake context must be in place.
	h.fact.wakeLock.Lock()
	require.NoError(t, h.fact.wakeCtx.Err(), "wake context must be recreated after HaltAll")
	h.fact.wakeLock.Unlock()
}

func Test_localWake_driveLoopLosslessUnderConcurrency(t *testing.T) {
	const instanceID = "test-drive-lossless"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)
	// The janitor is asserted by driveNewEvent; here we exercise localDrive
	// directly, so mark it asserted to keep the op log clean.
	h.orch.janitorAsserted.Store(true)

	// Hammer the drive from many goroutines. The buffered-1 notify channel
	// coalesces, but the reclaim handshake must guarantee that after the
	// LAST post there is always at least one subsequent turn: no post may
	// be lost to a loop that exited concurrently.
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 20 {
				h.orch.localDrive("new-event-tc-7", time.Now().Add(-time.Second))
			}
		})
	}
	wg.Wait()

	// After quiescing: at least one wake ran, the loop wound down, and no
	// notification is stranded in the channel with no loop to consume it.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		wakes := 0
		for _, op := range h.snapshotOps() {
			if strings.HasPrefix(op, "callReminder:") {
				wakes++
			}
		}
		assert.GreaterOrEqual(c, wakes, 1)
		if !h.orch.driveRunning.Load() {
			select {
			case <-h.orch.driveNotify:
				c.Errorf("stranded notification with no running drive loop")
			default:
			}
		}
	}, time.Second*5, time.Millisecond*5)

	require.NoError(t, h.fact.HaltAll(t.Context()))
}

// A terminal cached runtime state paired with a pending ExecutionStarted in
// the durable view is an impossible pairing in any consistent snapshot; the
// turn must not run from it (the engine would drop the work item and the
// follow-up save would resurrect the instance as PENDING with no name).
func Test_runWorkflow_terminalRstateWithPendingStartRecoverable(t *testing.T) {
	const instanceID = "test-inconsistent-pairing"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)

	// Rebuild the cached pairing: state holds ONLY a pending start in the
	// inbox (empty history), while rstate claims the instance completed.
	wfState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             "testapp",
		Namespace:         "default",
		WorkflowActorType: "dapr.internal.default.testapp.workflow",
		ActivityActorType: "dapr.internal.default.testapp.activity",
	})
	wfState.AddToInbox(&protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "TestWorkflow",
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
			},
		},
	})
	h.orch.state = wfState
	h.orch.rstate.CompletedEvent = &protos.ExecutionCompletedEvent{
		WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
	}

	completed, runErr := h.orch.runWorkflow(t.Context(), &actorapi.Reminder{Name: "start-es-1"})
	require.Error(t, runErr)
	assert.True(t, wferrors.IsRecoverable(runErr), "the guard must hand recovery to the re-fire")
	assert.Equal(t, todo.RunCompletedFalse, completed)
	assert.Nil(t, h.orch.state, "the stale cache must be invalidated")
	assert.NotContains(t, h.snapshotOps(), "save",
		"the inconsistent pairing must not produce any state save")
}

// The janitor's terminal self-delete is the recovery path for a lost
// retention create: it must re-assert retention (idempotent, deterministic
// name) before self-deleting.
func Test_janitor_terminalReassertsRetention(t *testing.T) {
	const instanceID = "test-janitor-retention"

	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)
	ttl := time.Hour
	h.orch.retentionPolicy = &config.WorkflowStateRetentionPolicy{AnyTerminal: &ttl}
	h.orch.rstate.CompletedEvent = &protos.ExecutionCompletedEvent{
		WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
	}

	require.NoError(t, h.orch.runJanitor(t.Context(), &actorapi.Reminder{Name: janitorReminderName}))
	ops := h.snapshotOps()
	assert.Contains(t, ops, "create:retention")
	assert.Contains(t, ops, "delete:new-event-janitor")
}

// The durable start reminder of a due-now fast-path start is a dormant
// backstop: due one redrive grace out, so it cannot fire beside the local
// drive that owns the turn.
func Test_startBackstopDueTime(t *testing.T) {
	const instanceID = "test-start-backstop"

	dueOf := func(t *testing.T, h *wakeHarness) time.Time {
		h.lock.Lock()
		defer h.lock.Unlock()
		for name, due := range h.dues {
			if strings.HasPrefix(name, "start-es-") {
				parsed, err := time.Parse(time.RFC3339Nano, due)
				require.NoError(t, err)
				return parsed
			}
		}
		require.Fail(t, "no start reminder created")
		return time.Time{}
	}

	t.Run("fast path due-now start is backstopped one redrive grace out", func(t *testing.T) {
		h := newWakeHarness(t, instanceID, true)
		before := time.Now()
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(),
			createRequestBytes(t, startEventFor(instanceID, before, nil))))

		due := dueOf(t, h)
		assert.False(t, due.Before(before.Add(pendingstart.RedriveGrace()/2)),
			"backstop due %s must not fire beside the local drive started at %s", due, before)
	})

	t.Run("fast path delayed start keeps its scheduled time", func(t *testing.T) {
		h := newWakeHarness(t, instanceID, true)
		at := time.Now().Add(time.Hour).Truncate(time.Second)
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(),
			createRequestBytes(t, startEventFor(instanceID, time.Now(), func(es *protos.ExecutionStartedEvent) {
				es.ScheduledStartTimestamp = timestamppb.New(at)
			}))))

		assert.True(t, dueOf(t, h).Equal(at))
	})

	t.Run("off the fast path the reminder is the driver and is due now", func(t *testing.T) {
		h := newWakeHarness(t, instanceID, false)
		before := time.Now()
		require.NoError(t, h.orch.createWorkflowInstance(t.Context(),
			createRequestBytes(t, startEventFor(instanceID, before, nil))))

		assert.True(t, dueOf(t, h).Before(before.Add(time.Second)))
	})
}
