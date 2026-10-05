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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// primeRunningWithExecID is primeRunning with a TaskExecutionId recorded on
// the outstanding TaskScheduled, as every SDK since v0.12 sends.
func (h *wakeHarness) primeRunningWithExecID(t *testing.T, instanceID string, scheduled int32, execID string) {
	t.Helper()
	h.primeRunning(t, instanceID, scheduled)
	h.orch.state.FindHistoryEventByID(scheduled).GetTaskScheduled().TaskExecutionId = execID
}

func taskCompletedWithExecID(scheduled int32, execID string) *backend.HistoryEvent {
	e := taskCompletedEvent(scheduled)
	e.GetTaskCompleted().TaskExecutionId = execID
	return e
}

func Test_classifyEvent_supersededSchedulingIsDropped(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-superseded"
	h := newWakeHarness(t, instanceID, true)
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
	h.saved = true

	// Task 7 was rescheduled under exec-B, so the completion of its first
	// scheduling can never be consumed: acknowledge it and drop it.
	stale := taskCompletedWithExecID(7, "exec-A")
	for _, canFold := range []bool{false, true} {
		a := h.orch.classifyEvent(stale, h.orch.state, completionSender{}, canFold)
		require.NoError(t, a.err, "canFold=%v", canFold)
		assert.Equal(t, admitDrop, a.outcome, "canFold=%v", canFold)
		assert.Contains(t, a.reason, "superseded scheduling of task 7", "canFold=%v", canFold)
	}

	require.NoError(t, h.orch.addWorkflowEvent(t.Context(), stale, completionSender{}))
	assert.Empty(t, h.orch.state.Inbox, "the straggler must not be persisted")
	assert.Empty(t, h.orch.foldPending)
}

func Test_classifyEvent_currentSchedulingIsAdmitted(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-current"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")

	current := taskCompletedWithExecID(7, "exec-B")
	assert.Equal(t, admitInbox, h.orch.classifyEvent(current, h.orch.state, completionSender{}, false).outcome)
	assert.Equal(t, admitFold, h.orch.classifyEvent(current, h.orch.state, completionSender{}, true).outcome)

	// Senders that predate execution ids, and schedulings recorded without
	// one, keep matching by task id alone.
	unversioned := taskCompletedEvent(7)
	assert.Equal(t, admitFold, h.orch.classifyEvent(unversioned, h.orch.state, completionSender{}, true).outcome)
	h.orch.state.FindHistoryEventByID(7).GetTaskScheduled().TaskExecutionId = ""
	assert.Equal(t, admitFold, h.orch.classifyEvent(current, h.orch.state, completionSender{}, true).outcome)
}

func Test_classifyEvent_absentSchedulingTakesTheInbox(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-absent"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")

	// The scheduling row of task 8 may still be committing: the completion
	// is admitted to the durable inbox, never dropped and never folded.
	early := taskCompletedWithExecID(8, "exec-C")
	for _, canFold := range []bool{false, true} {
		a := h.orch.classifyEvent(early, h.orch.state, completionSender{}, canFold)
		assert.Equal(t, admitInbox, a.outcome, "canFold=%v", canFold)
		assert.Empty(t, a.reason, "canFold=%v", canFold)
	}
}

func Test_classifyEvent_activityFromAnotherExecution(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-other-execution"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
	h.orch.getExecutionStartedEvent(h.orch.state).WorkflowInstance.ExecutionId = wrapperspb.String("gen-2")

	// Task IDs restart on ContinueAsNew, so a result dispatched by another
	// execution is dropped even while this generation has not reached its
	// task ID, where it would otherwise be held as an early result.
	result := taskCompletedWithExecID(8, "exec-C")
	for _, canFold := range []bool{false, true} {
		a := h.orch.classifyEvent(result, h.orch.state, completionSender{parentExecutionID: "gen-1"}, canFold)
		assert.Equal(t, admitDrop, a.outcome, "canFold=%v", canFold)
		assert.Contains(t, a.reason, "previous execution", "canFold=%v", canFold)

		a = h.orch.classifyEvent(result, h.orch.state, completionSender{parentExecutionID: "gen-2"}, canFold)
		assert.Equal(t, admitInbox, a.outcome, "this execution's result: canFold=%v", canFold)

		a = h.orch.classifyEvent(result, h.orch.state, completionSender{}, canFold)
		assert.Equal(t, admitInbox, a.outcome, "a sender that predates the stamp: canFold=%v", canFold)
	}
}

func Test_classifyEvent_provenStragglers(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-proven"

	t.Run("the id sequence passed the task without scheduling it", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.fact.fastPath = true
		h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
		for _, canFold := range []bool{false, true} {
			a := h.orch.classifyEvent(taskCompletedWithExecID(3, "exec-A"), h.orch.state, completionSender{}, canFold)
			require.NoError(t, a.err, "canFold=%v", canFold)
			assert.Equal(t, admitDrop, a.outcome, "canFold=%v", canFold)
			assert.Contains(t, a.reason, "without scheduling", "canFold=%v", canFold)
		}
	})

	t.Run("the workflow has completed", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
		h.orch.state.AddToHistory(&protos.HistoryEvent{
			EventId:   8,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{}},
		})
		a := h.orch.classifyEvent(taskCompletedWithExecID(9, "exec-C"), h.orch.state, completionSender{}, false)
		assert.Equal(t, admitDrop, a.outcome)
		assert.Equal(t, "the workflow has completed", a.reason)
	})
}

// A dropped activity result still clears the await: createIfCompleted
// refuses reuse of a completed instance's ID while a result is awaited, and
// a result that has been judged is no longer in flight.
func Test_admitEvent_droppedActivityResultSettlesTheAwait(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-await"
	h := newWakeHarness(t, instanceID, false)
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
	h.saved = true
	h.orch.state.AddToHistory(&protos.HistoryEvent{
		EventId:   8,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionCompleted{ExecutionCompleted: &protos.ExecutionCompletedEvent{}},
	})
	h.orch.activityResultAwaited.Store(true)

	entry, err := h.orch.admitEvent(t.Context(), taskCompletedWithExecID(9, "exec-C"), completionSender{}, false)
	require.NoError(t, err, "the result is acked and dropped")
	assert.Nil(t, entry)
	assert.Empty(t, h.orch.state.Inbox)
	assert.False(t, h.orch.activityResultAwaited.Load(), "a dropped result must still clear the await")
}

func Test_cleanupWorkflowStateInternal_dropsTheCache(t *testing.T) {
	t.Parallel()
	const instanceID = "test-purge-cache"
	h := newWakeHarness(t, instanceID, false)
	h.primeRunning(t, instanceID, 7)
	h.saved = true

	// Hold the actor lock so the asynchronous deactivation cannot run: a
	// create for the same ID that wins the lock first must find no cache.
	unlock, err := h.orch.lock.ContextLock(t.Context())
	require.NoError(t, err)
	defer unlock()

	require.NoError(t, h.orch.cleanupWorkflowStateInternal(t.Context(), h.orch.state, true))
	assert.Nil(t, h.orch.state, "the purged state must not be served from the cache")
	assert.Nil(t, h.orch.rstate)
	assert.Nil(t, h.orch.ometa)
}
