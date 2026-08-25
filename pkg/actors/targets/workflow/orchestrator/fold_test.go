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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func eventRaisedEvent(name string) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_EventRaised{
			EventRaised: &protos.EventRaisedEvent{Name: name},
		},
	}
}

func Test_fold_submitHoldsWithoutSave(t *testing.T) {
	const instanceID = "test-fold-hold"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)

	entry, err := h.orch.addWorkflowEventMaybeFold(t.Context(), taskCompletedEvent(7))
	require.NoError(t, err)
	require.NotNil(t, entry, "a sender-retried completion must be held for folding")

	// No inbox save happened: the only ops are the janitor assert (create)
	// from the drive arming; specifically no "save".
	for _, op := range h.snapshotOps() {
		assert.NotEqual(t, "save", op, "the fold must not commit the inbox")
	}
	assert.Len(t, h.orch.foldPending, 1)
	assert.Empty(t, h.orch.state.Inbox, "the event must not touch the durable inbox")
}

func Test_fold_externalEventKeepsInboxPath(t *testing.T) {
	const instanceID = "test-fold-external"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)

	entry, err := h.orch.addWorkflowEventMaybeFold(t.Context(), eventRaisedEvent("go"))
	require.NoError(t, err)
	assert.Nil(t, entry, "external events have no sender durability and must use the durable inbox")
	assert.Contains(t, h.snapshotOps(), "save", "the inbox path must commit")
	assert.Empty(t, h.orch.foldPending)
}

func Test_fold_duplicatePendingJoins(t *testing.T) {
	const instanceID = "test-fold-dup"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)

	entry, err := h.orch.addWorkflowEventMaybeFold(t.Context(), taskCompletedEvent(7))
	require.NoError(t, err)
	require.NotNil(t, entry)

	// A redelivery of the same completion while the first is pending must
	// join the pending entry (waiting on the same commit), never be acked
	// early and never double-held: an early ack would stop the retry chain
	// while the completion exists only in memory.
	entry2, err := h.orch.addWorkflowEventMaybeFold(t.Context(), taskCompletedEvent(7))
	require.NoError(t, err)
	require.Same(t, entry, entry2, "a retry must join the pending entry")
	assert.Len(t, h.orch.foldPending, 1)
}

func Test_fold_takeCapAndOrder(t *testing.T) {
	const instanceID = "test-fold-take"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 1)

	total := maxFoldPerTurn + 5
	for i := range total {
		h.orch.foldPending = append(h.orch.foldPending, &foldEntry{
			event:     taskCompletedEvent(int32(i + 10)),
			gen:       1,
			committed: make(chan struct{}),
		})
	}

	taken := h.orch.foldTake(1)
	assert.Len(t, taken, maxFoldPerTurn, "one turn folds at most maxFoldPerTurn completions")
	assert.Len(t, h.orch.foldPending, 5, "overflow stays pending")
	assert.Equal(t, int32(10), taken[0].event.GetTaskCompleted().GetTaskScheduledId(), "arrival order preserved")

	rest := h.orch.foldTake(1)
	assert.Len(t, rest, 5)
	assert.Empty(t, h.orch.foldTake(1))
}

func Test_fold_ackNackFlush(t *testing.T) {
	mk := func(n int) []*foldEntry {
		out := make([]*foldEntry, n)
		for i := range out {
			out[i] = &foldEntry{event: taskCompletedEvent(int32(i)), committed: make(chan struct{})}
		}
		return out
	}

	wait := func(e *foldEntry) error {
		<-e.committed
		return e.err
	}

	acked := mk(3)
	foldAck(acked)
	for _, e := range acked {
		require.NoError(t, wait(e))
	}

	nacked := mk(2)
	foldNack(nacked, errors.New("turn failed"))
	for _, e := range nacked {
		err := wait(e)
		require.Error(t, err)
		assert.True(t, wferrors.IsRecoverable(err), "nacks must be recoverable so the sender retries")
	}

	// Nil error still produces a recoverable nack (e.g. a stalled turn
	// returning nil).
	nacked2 := mk(1)
	foldNack(nacked2, nil)
	assert.True(t, wferrors.IsRecoverable(wait(nacked2[0])))

	// Broadcast: a retry that joined a pending entry observes the same
	// resolution as the original submitter.
	joined := mk(1)
	obs := make(chan error, 2)
	for range 2 {
		go func() { obs <- wait(joined[0]) }()
	}
	foldAck(joined)
	require.NoError(t, <-obs)
	require.NoError(t, <-obs)

	const instanceID = "test-fold-flush"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.orch.foldPending = mk(2)
	h.orch.foldFlush()
	assert.Empty(t, h.orch.foldPending)
}

func Test_fold_unresolvedTreatsPendingAsResolved(t *testing.T) {
	state := testState(t)
	state.AddToHistory(startedEvent())
	state.AddToHistory(taskScheduledEvent(1))
	state.AddToHistory(taskScheduledEvent(2))

	pending := []*backend.HistoryEvent{taskCompletedEvent(1)}
	unresolved := unresolvedScheduledTasks(state, pending)
	require.Len(t, unresolved, 1)
	assert.Equal(t, int32(2), unresolved[0].GetEventId(),
		"a completion held for folding must suppress janitor re-dispatch of its task")
}

func childCompletedEvent(scheduled int32) *backend.HistoryEvent {
	return &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ChildWorkflowInstanceCompleted{
			ChildWorkflowInstanceCompleted: &protos.ChildWorkflowInstanceCompletedEvent{
				TaskScheduledId: scheduled,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}
}

// Child completions must not fold (lock-cycle risk); they take the durable
// inbox path.
func Test_fold_childCompletionKeepsInboxPath(t *testing.T) {
	const instanceID = "test-fold-child"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)

	entry, err := h.orch.addWorkflowEventMaybeFold(t.Context(), childCompletedEvent(7))
	require.NoError(t, err)
	assert.Nil(t, entry, "child completions must not be held for folding")
	assert.Contains(t, h.snapshotOps(), "save", "the inbox path must commit")
	assert.Empty(t, h.orch.foldPending)
}

// Stale-generation entries are dropped-and-acked at take.
func Test_fold_takeDropsStaleGeneration(t *testing.T) {
	const instanceID = "test-fold-stalegen"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)

	stale := &foldEntry{event: taskCompletedEvent(10), gen: 1, committed: make(chan struct{})}
	fresh := &foldEntry{event: taskCompletedEvent(11), gen: 2, committed: make(chan struct{})}
	h.orch.foldPending = []*foldEntry{stale, fresh}

	taken := h.orch.foldTake(2)
	require.Len(t, taken, 1)
	assert.Same(t, fresh, taken[0], "only current-generation entries may be taken")

	select {
	case <-stale.committed:
		require.NoError(t, stale.err, "the stale entry is acked (acceptance-and-drop), not nacked into a retry loop")
	default:
		t.Fatal("the stale entry must be resolved at take")
	}
	assert.Empty(t, h.orch.foldPending)
}

// A TaskExecutionId mismatch marks a straggler; it must not fold.
func Test_fold_executionIDMismatchKeepsInboxPath(t *testing.T) {
	const instanceID = "test-fold-execid"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)
	h.orch.state.History[1].GetTaskScheduled().TaskExecutionId = "exec-A"
	h.orch.state.AddToHistory(&protos.HistoryEvent{
		EventId:   8,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskScheduled{
			TaskScheduled: &protos.TaskScheduledEvent{Name: "act", TaskExecutionId: "exec-A"},
		},
	})

	mismatch := taskCompletedEvent(7)
	mismatch.GetTaskCompleted().TaskExecutionId = "exec-B"
	entry, err := h.orch.addWorkflowEventMaybeFold(t.Context(), mismatch)
	require.NoError(t, err)
	assert.Nil(t, entry, "a mismatched execution id must not fold")
	assert.Empty(t, h.orch.foldPending)

	match := taskCompletedEvent(8)
	match.GetTaskCompleted().TaskExecutionId = "exec-A"
	entry, err = h.orch.addWorkflowEventMaybeFold(t.Context(), match)
	require.NoError(t, err)
	assert.NotNil(t, entry, "a matching execution id folds as usual")
}

// The payload stall guard must count folded completions.
func Test_workflowPayloadOversize_includesFoldedBytes(t *testing.T) {
	const instanceID = "test-fold-payload"
	h := newWakeHarness(t, instanceID, true)
	h.fact.fastPath = true
	h.primeRunning(t, instanceID, 7)
	h.orch.maxRequestBodySize = 2048

	_, _, oversize := h.orch.workflowPayloadOversize(t.Context(), h.orch.state, nil, "wf")
	require.False(t, oversize, "the primed state alone is well under the threshold")

	big := taskCompletedEvent(7)
	big.GetTaskCompleted().Result = wrapperspb.String(strings.Repeat("x", 4096))
	_, _, oversize = h.orch.workflowPayloadOversize(t.Context(), h.orch.state, []*backend.HistoryEvent{big}, "wf")
	assert.True(t, oversize, "folded completions must count toward the stall threshold")
}
