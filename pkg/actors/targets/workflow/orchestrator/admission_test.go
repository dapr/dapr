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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	statefake "github.com/dapr/dapr/pkg/actors/state/fake"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
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

func Test_classifyEvent_supersededSchedulingIsRefusedRecoverably(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-superseded"
	h := newWakeHarness(t, instanceID, true)
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
	h.saved = true

	// Task 7 was rescheduled under exec-B; the completion of its first
	// scheduling arrives late. A read lagging a ContinueAsNew boundary looks
	// the same, so the sender is asked to retry rather than told to drop.
	stale := taskCompletedWithExecID(7, "exec-A")
	for _, canFold := range []bool{false, true} {
		a := h.orch.classifyEvent(stale, h.orch.state, completionSender{}, canFold)
		require.ErrorContains(t, a.err, common.ErrSchedulingSuperseded.Error(), "canFold=%v", canFold)
		assert.True(t, wferrors.IsRecoverable(a.err), "canFold=%v", canFold)
	}

	err := h.orch.addWorkflowEvent(t.Context(), stale, completionSender{})
	require.ErrorContains(t, err, common.ErrSchedulingSuperseded.Error())
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
			require.ErrorContains(t, a.err, common.ErrSchedulingSuperseded.Error(), "canFold=%v", canFold)
			assert.ErrorContains(t, a.err, "without scheduling", "canFold=%v", canFold)
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

// A dropped activity result still settles the await: createIfCompleted
// refuses reuse of a completed instance's ID while a result is awaited.
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

// A straggler from a superseded scheduling is not the result the await is
// for: refusing it must leave the guard against reusing the ID in place.
func Test_admitEvent_droppedStragglerKeepsTheAwait(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-await-straggler"
	h := newWakeHarness(t, instanceID, false)
	h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
	h.saved = true
	h.orch.activityResultAwaited.Store(true)

	entry, err := h.orch.admitEvent(t.Context(), taskCompletedWithExecID(7, "exec-A"), completionSender{}, false)
	require.ErrorContains(t, err, common.ErrSchedulingSuperseded.Error(), "the straggler is refused for the sender to retry, then drop")
	assert.Nil(t, entry)
	assert.Empty(t, h.orch.state.Inbox)
	assert.True(t, h.orch.activityResultAwaited.Load(), "a superseded scheduling's result must not settle the await")
}

// Under history signing an unmatched activity completion is verified against
// durable state. A completion for a task that state does not show, below
// every recorded id, may be ahead of its scheduling's commit and is refused
// recoverably however old it is; one the durable history proves can never be
// consumed is dropped.
func Test_verifyAndAbsorbAttestation_absentSchedulingIsRefusedRecoverably(t *testing.T) {
	t.Parallel()
	const instanceID = "test-admit-late-commit"

	newSigned := func(t *testing.T) *wakeHarness {
		t.Helper()
		h := newWakeHarness(t, instanceID, false)
		h.fact.signer = testAddSignerWithTrust(t, true)
		h.orch.signing = &signing.Signing{
			Signer:            h.fact.signer,
			Namespace:         "default",
			ActorID:           instanceID,
			ActorType:         h.fact.actorType,
			ActivityActorType: h.fact.activityActorType,
			Reminders:         h.fact.reminders,
		}
		h.primeRunningWithExecID(t, instanceID, 7, "exec-B")
		h.orch.actorState = fakeStoreServingSigned(t, h.orch, h.orch.state)
		return h
	}
	attested := func(scheduled int32, at time.Time) *backend.HistoryEvent {
		e := taskCompletedWithExecID(scheduled, "exec-C")
		e.Timestamp = timestamppb.New(at)
		e.GetTaskCompleted().Attestation = &backend.ActivityCompletionAttestation{}
		return e
	}

	for name, age := range map[string]time.Duration{"young": 0, "an hour old": time.Hour} {
		t.Run("a completion "+name+" is refused recoverably", func(t *testing.T) {
			t.Parallel()
			h := newSigned(t)
			err := h.orch.addWorkflowEvent(t.Context(), attested(8, time.Now().Add(-age)), completionSender{})
			require.Error(t, err)
			assert.True(t, wferrors.IsRecoverable(err), "the sender must retry once the scheduling commits: %v", err)
			require.NotErrorIs(t, err, api.ErrInstanceNotFound)
			assert.Empty(t, h.orch.state.Inbox, "nothing is persisted before the scheduling is durable")
			assert.False(t, h.orch.state.HasTamperMarker())
		})
	}

	t.Run("a completion the id sequence has passed is refused as superseded", func(t *testing.T) {
		t.Parallel()
		h := newSigned(t)
		// The cache is stale: it does not hold task 7 yet, so the verdict comes from the durable load.
		h.orch.state.History = h.orch.state.History[:1]
		err := h.orch.addWorkflowEvent(t.Context(), attested(3, time.Now()), completionSender{})
		require.ErrorContains(t, err, common.ErrSchedulingSuperseded.Error(), "the sender retries for its window, then drops")
		assert.True(t, wferrors.IsRecoverable(err))
		assert.Empty(t, h.orch.state.Inbox)
		assert.False(t, h.orch.state.HasTamperMarker())
	})
}

// fakeStoreServingSigned signs the state's history with the orchestrator's
// signer and serves the resulting save request from a fake store, so a
// durable reload verifies against exactly what a signing host would have
// committed.
func fakeStoreServingSigned(t *testing.T, o *orchestrator, state *wfenginestate.State) *statefake.Fake {
	t.Helper()
	require.NoError(t, o.signing.SignNewEvents(state))
	req, err := state.GetSaveRequest(o.actorID)
	require.NoError(t, err)
	rows := make(map[string][]byte, len(req.Operations))
	for _, op := range req.Operations {
		if u, ok := op.Request.(actorapi.TransactionalUpsert); ok {
			rows[u.Key], _ = u.Value.([]byte)
		}
	}
	etag := "signed-etag"
	return statefake.New().
		WithGetFn(func(_ context.Context, req *actorapi.GetStateRequest, _ bool) (*actorapi.StateResponse, error) {
			return &actorapi.StateResponse{Data: rows[req.Key], ETag: &etag}, nil
		}).
		WithGetBulkFn(func(_ context.Context, req *actorapi.GetBulkStateRequest, _ bool) (actorapi.BulkStateResponse, error) {
			res := make(actorapi.BulkStateResponse, len(req.Keys))
			for _, k := range req.Keys {
				res[k] = actorapi.BulkStateEntry{Data: rows[k]}
			}
			return res, nil
		})
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
