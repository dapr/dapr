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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"google.golang.org/protobuf/types/known/timestamppb"

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
			committed: make(chan struct{}),
		})
	}

	taken := h.orch.foldTake()
	assert.Len(t, taken, maxFoldPerTurn, "one turn folds at most maxFoldPerTurn completions")
	assert.Len(t, h.orch.foldPending, 5, "overflow stays pending")
	assert.Equal(t, int32(10), taken[0].event.GetTaskCompleted().GetTaskScheduledId(), "arrival order preserved")

	rest := h.orch.foldTake()
	assert.Len(t, rest, 5)
	assert.Empty(t, h.orch.foldTake())
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
