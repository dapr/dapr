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
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// An activity-result reminder may carry only an activity result, or a child
// completion this app synthesised for itself: any other event under that
// name is acked and dropped, from any sender, before it can reach the inbox
// (a TimerFired there panics the save).
func Test_handleReminder_activityResultDropsNonResultPayloads(t *testing.T) {
	t.Parallel()

	payloads := map[string]*protos.HistoryEvent{
		"EventRaised": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: "evt"},
		}},
		"ExecutionTerminated": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{},
		}},
		"TimerFired": {EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_TimerFired{
			TimerFired: &protos.TimerFiredEvent{TimerId: 7},
		}},
	}
	childFailed := func() *protos.HistoryEvent {
		return &protos.HistoryEvent{EventId: -1, Timestamp: timestamppb.Now(), EventType: &protos.HistoryEvent_ChildWorkflowInstanceFailed{
			ChildWorkflowInstanceFailed: &protos.ChildWorkflowInstanceFailedEvent{TaskScheduledId: 7},
		}}
	}

	t.Run("ChildWorkflowInstanceFailed from another app is dropped", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, "test-forged-child", true)
		h.primeRunning(t, "test-forged-child", 7)
		h.saved = true
		data, err := anypb.New(childFailed())
		require.NoError(t, err)
		require.NoError(t, h.orch.handleReminder(t.Context(), &actorapi.Reminder{
			Name: "activity-result-forged", ActorType: h.orch.actorType, ActorID: h.orch.actorID, Data: data, SourceAppID: "other",
		}))
		assert.Empty(t, h.orch.state.Inbox)
	})

	t.Run("ChildWorkflowInstanceFailed from own app is admitted", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, "test-own-child", true)
		h.primeRunning(t, "test-own-child", 7)
		h.saved = true
		data, err := anypb.New(childFailed())
		require.NoError(t, err)
		require.NoError(t, h.orch.handleReminder(t.Context(), &actorapi.Reminder{
			Name: "activity-result-own", ActorType: h.orch.actorType, ActorID: h.orch.actorID, Data: data, SourceAppID: "testapp",
		}))
		assert.Len(t, h.orch.state.Inbox, 1)
	})

	for name, ev := range payloads {
		for _, sender := range []string{"other", "testapp", ""} {
			t.Run(name+" from '"+sender+"'", func(t *testing.T) {
				t.Parallel()
				h := newWakeHarness(t, "test-forged-"+name, true)
				h.primeRunning(t, "test-forged-"+name, 7)
				h.saved = true

				data, err := anypb.New(ev)
				require.NoError(t, err)
				require.NoError(t, h.orch.handleReminder(t.Context(), &actorapi.Reminder{
					Name:        "activity-result-forged",
					ActorType:   h.orch.actorType,
					ActorID:     h.orch.actorID,
					Data:        data,
					SourceAppID: sender,
				}), "the reminder is acked so the one-shot job is deleted")
				assert.Empty(t, h.orch.state.Inbox)
				assert.NotContains(t, h.snapshotOps(), "save")
			})
		}
	}
}

// A result from another app for a task the history has not scheduled yet is
// refused as not durable (retried, then dropped) rather than parked in the
// inbox, where it would be consumed with no creator check once the task is
// scheduled.
func Test_classifyEvent_crossAppResultAheadOfScheduling(t *testing.T) {
	t.Parallel()

	const instanceID = "test-admit-ahead"
	newHarness := func(t *testing.T) *wakeHarness {
		t.Helper()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		h.saved = true
		return h
	}
	schedule9 := func(h *wakeHarness, targetAppID string) {
		h.orch.state.AddToHistory(&protos.HistoryEvent{
			EventId:   9,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskScheduled{TaskScheduled: &protos.TaskScheduledEvent{Name: "act"}},
			Router:    &protos.TaskRouter{SourceAppID: "testapp", TargetAppID: &targetAppID},
		})
	}
	ahead := func() *backend.HistoryEvent { return taskCompletedEvent(9) }

	t.Run("cross-app result for an unscheduled task is not durable", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		sender := completionSender{appID: "other"}
		for _, canFold := range []bool{false, true} {
			a := h.orch.classifyEvent(ahead(), h.orch.state, sender, canFold)
			require.Error(t, a.err, "canFold=%v", canFold)
			assert.True(t, common.IsSchedulingNotDurable(a.err), "canFold=%v: %v", canFold, a.err)
			assert.Equal(t, admitDrop, a.outcome, "canFold=%v", canFold)
		}
		err := h.orch.addWorkflowEvent(t.Context(), ahead(), sender)
		require.Error(t, err)
		assert.True(t, common.IsSchedulingNotDurable(err), "%v", err)
		assert.Empty(t, h.orch.state.Inbox, "nothing is persisted")
		assert.NotContains(t, h.snapshotOps(), "save")
	})

	t.Run("own app and unknown creator keep the inbox path", func(t *testing.T) {
		t.Parallel()
		for _, appID := range []string{"testapp", ""} {
			h := newHarness(t)
			a := h.orch.classifyEvent(ahead(), h.orch.state, completionSender{appID: appID}, false)
			require.NoError(t, a.err, "sender %q", appID)
			assert.Equal(t, admitInbox, a.outcome, "sender %q", appID)
		}
	})

	t.Run("once scheduled, the dispatch target is admitted", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		schedule9(h, "other")
		a := h.orch.classifyEvent(ahead(), h.orch.state, completionSender{appID: "other"}, false)
		require.NoError(t, a.err)
		assert.Equal(t, admitInbox, a.outcome)
		require.NoError(t, h.orch.addWorkflowEvent(t.Context(), ahead(), completionSender{appID: "other"}))
		assert.Len(t, h.orch.state.Inbox, 1)
	})

	t.Run("once scheduled, another app is dropped", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		schedule9(h, "appB")
		a := h.orch.classifyEvent(ahead(), h.orch.state, completionSender{appID: "other"}, false)
		require.NoError(t, a.err)
		assert.Equal(t, admitDrop, a.outcome)
		assert.Equal(t, "it was sent by app 'other' but the task was dispatched to 'appB'", a.reason)
		require.NoError(t, h.orch.addWorkflowEvent(t.Context(), ahead(), completionSender{appID: "other"}), "a drop is acked")
		assert.Empty(t, h.orch.state.Inbox)
	})
}

// The event timestamp bounds the retry of a cross-app result refused as not
// durable, and the creator chooses it: a stamp far in the future must read as
// expired, not as never expiring, or the retry-forever reminder would refire
// every second for good.
func Test_handleReminder_crossAppResultAheadOfSchedulingRetryWindow(t *testing.T) {
	t.Parallel()

	const instanceID = "test-ahead-window"
	fire := func(t *testing.T, stamp time.Time) error {
		t.Helper()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		// The fire drops the cache and reloads, so the store must serve
		// the same history.
		h.orch.actorState = fakeStoreServingState(t, 1, h.orch.state.History, nil)
		ev := taskCompletedEvent(9)
		ev.Timestamp = timestamppb.New(stamp)
		data, err := anypb.New(ev)
		require.NoError(t, err)
		err = h.orch.handleReminder(t.Context(), &actorapi.Reminder{
			Name: "activity-result-ahead", ActorType: h.orch.actorType, ActorID: h.orch.actorID, Data: data, SourceAppID: "other",
		})
		assert.NotContains(t, h.snapshotOps(), "save", "nothing is persisted either way")
		return err
	}

	t.Run("a current stamp is retried", func(t *testing.T) {
		t.Parallel()
		err := fire(t, time.Now())
		require.Error(t, err)
		assert.True(t, common.IsSchedulingNotDurable(err), "%v", err)
	})

	t.Run("a stamp an hour ahead is dropped and acked", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, fire(t, time.Now().Add(time.Hour)))
	})

	t.Run("a stamp an hour behind is dropped and acked", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, fire(t, time.Now().Add(-time.Hour)))
	})
}

// A forged result dropped by the creator check must not release the
// instance-ID reuse guard: the real result it impersonates is still in
// flight, and the guard is what refuses recreating the completed instance
// under it (createIfCompleted).
func Test_admitEvent_unauthorizedDropKeepsTheAwait(t *testing.T) {
	t.Parallel()

	const instanceID = "test-admit-forged-await"
	h := newWakeHarness(t, instanceID, true)
	h.primeRunning(t, instanceID, 7)
	h.saved = true
	appB := "appB"
	h.orch.state.FindHistoryEventByID(7).Router = &protos.TaskRouter{SourceAppID: "testapp", TargetAppID: &appB}
	h.orch.activityResultAwaited.Store(true)

	entry, err := h.orch.admitEvent(t.Context(), taskCompletedEvent(7), completionSender{appID: "evil"}, false)
	require.NoError(t, err, "the forged result is acked and dropped")
	assert.Nil(t, entry)
	assert.Empty(t, h.orch.state.Inbox)
	assert.True(t, h.orch.activityResultAwaited.Load(), "a forged result must not release the reuse guard")

	entry, err = h.orch.admitEvent(t.Context(), taskCompletedEvent(7), completionSender{appID: appB}, false)
	require.NoError(t, err)
	assert.Nil(t, entry)
	assert.Len(t, h.orch.state.Inbox, 1)
	assert.False(t, h.orch.activityResultAwaited.Load(), "the real result settles the await")
}
