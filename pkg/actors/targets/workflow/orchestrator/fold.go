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
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/events"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/backend"
	dtdedup "github.com/dapr/durabletask-go/backend/runtimestate/dedup"
)

// maxFoldPerTurn bounds how many held completions one turn persists: the
// turn's Multi has no chunking (see the TODO in state.GetSaveRequest), so a
// wide fan-in must amortize across turns rather than grow one transaction
// unboundedly. Overflow stays pending and the taking turn re-drives.
const maxFoldPerTurn = 128

// foldEntry is one sender-retried completion held in memory awaiting its
// folding turn. committed is closed exactly once when the entry resolves,
// with err set first: nil after the turn's commit persisted the event into
// history, or the error to return to the senders (whose retries are the
// durability). Broadcast semantics: the original submitter AND any retry
// that found the entry already pending all wait on the same resolution; a
// retry acked before the commit would stop the only durable re-driver.
type foldEntry struct {
	event *backend.HistoryEvent
	// gen is state.Generation at submit time; foldTake drops-and-acks
	// entries from a previous generation (ContinueAsNew resets event ids,
	// so kind+id matching across generations is invalid).
	gen       uint64
	err       error
	committed chan struct{}
}

// invokeAddEventFold is the WorkflowsFastPath entry point for
// AddWorkflowEvent invocations. Sender-retried completions skip the durable
// inbox commit entirely: the event is held in memory, the next turn persists
// it straight into history inside its single existing Multi, and only after
// that commit is the sender acked. Everything else (external events,
// duplicates, stalled or tombstoned instances, gate off at the factory)
// takes the durable inbox path; classifyEvent owns the decision.
//
// It manages the actor lock itself, mirroring InvokeStream: validation and
// the pending append run under the lock, then the lock is RELEASED before
// blocking on the commit signal, because the folding turn needs that same
// lock. The resiliency post-lock retry wrapper is deliberately skipped: a
// retry after the early unlock would re-enter unlocked, and the sender's own
// retry chain owns redelivery.
func (o *orchestrator) invokeAddEventFold(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	var ev backend.HistoryEvent
	if err := proto.Unmarshal(req.GetMessage().GetData().GetValue(), &ev); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AddWorkflowEvent HistoryEvent: %w", err)
	}

	unlock, err := o.contextLockMeasured(ctx, "method")
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method for workflow '%s': %w", o.actorID, err)
	}
	unlockOnce := sync.OnceFunc(unlock)
	defer unlockOnce()

	// Under the lock, like the normal handleInvoke path: the policy check
	// reads mutable cached workflow state, and authorizing before the lock
	// could approve a completion against an execution that a concurrent
	// turn replaces before this one applies.
	if err = o.checkAccessPolicy(ctx, req.GetMessage().GetMethod(), req.GetMessage().GetData().GetValue(), &ev, nil, req.GetMetadata()); err != nil {
		return nil, err
	}

	entry, err := o.admitEvent(ctx, &ev, senderFromMetadata(req.GetMetadata()), true)
	if err != nil {
		return nil, err
	}

	if entry != nil {
		// The event is held and its turn is armed: hand the lock to the
		// turn and wait for the commit outside it.
		unlockOnce()

		start := time.Now()
		select {
		case <-ctx.Done():
			// The sender gave up; the entry stays pending and is either
			// committed by the armed turn (the redelivery then dedups) or
			// flushed at deactivation.
			diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFoldNacked)
			return nil, ctx.Err()
		case <-time.After(o.foldWaitTimeout):
			diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFoldNacked)
			return nil, wferrors.NewRecoverable(fmt.Errorf("workflow actor '%s': timed out waiting for the folding turn to commit", o.actorID))
		case <-entry.committed:
			err = entry.err
			diag.DefaultWorkflowMonitoring.WorkflowCompletionsFoldWait(context.Background(), float64(time.Since(start))/float64(time.Millisecond))
			if err != nil {
				return nil, err
			}
		}
	}

	return &internalsv1pb.InternalInvokeResponse{
		Status:  &internalsv1pb.Status{Code: http.StatusOK},
		Message: &commonv1pb.InvokeResponse{Data: &anypb.Any{}},
	}, nil
}

// foldSubmit holds e for the next turn and arms the local drive. Called with
// the actor lock held. The caller must wait on the returned entry after
// releasing the lock.
func (o *orchestrator) foldSubmit(ctx context.Context, e *backend.HistoryEvent, state *wfenginestate.State) *foldEntry {
	entry := &foldEntry{event: e, gen: state.Generation, committed: make(chan struct{})}
	o.foldPending = append(o.foldPending, entry)

	// Best effort: the sender is only acked after commit, so the pending
	// window needs no durable cover of its own (the sender retry is the
	// net), but an armed janitor keeps the wider fast-path contract uniform.
	if jerr := o.ensureJanitor(ctx, state); jerr != nil {
		log.Debugf("Workflow actor '%s': janitor assert failed on fold submit (sender retry remains the net): %v", o.actorID, jerr)
	}

	if dropFoldDriveForTest() {
		log.Warnf("TEST INJECTION: dropping the fold drive arm for instance '%s'", o.actorID)
		return entry
	}

	dueTime := newEventDueTime(e, state)
	o.localDrive(events.EventReminderName(reminderPrefixNewEvent, e), dueTime)
	return entry
}

// testDropFoldDrives is a test-only fault injection: the first N fold
// submissions skip arming their folding turn's local drive, reproducing an
// arm lost to the wakeCtx cancellation window (a HaltAll racing the submit at
// a placement handoff) or a failed drive that exhausted its retries. The
// held completion is then committed by nothing unless its sender re-delivers
// or the janitor drives a turn. Not a supported production knob.
var testDropFoldDrives = sync.OnceValue(func() int64 {
	v := os.Getenv("DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES")
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		log.Warnf("Ignoring invalid DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES %q", v)
		return 0
	}
	return n
})

// droppedFoldDrives counts the fold drive arms dropped by the test-only
// DAPR_WORKFLOW_TEST_DROP_FOLD_DRIVES injection.
var droppedFoldDrives atomic.Int64

func dropFoldDriveForTest() bool {
	budget := testDropFoldDrives()
	if budget == 0 {
		return false
	}
	return droppedFoldDrives.Add(1) <= budget
}

// foldPendingEntry returns the held entry with the same resolution key as
// e, or nil. Lock held by caller.
func (o *orchestrator) foldPendingEntry(e *backend.HistoryEvent) *foldEntry {
	kind, id, ok := dtdedup.Of(e)
	if !ok {
		return nil
	}
	for _, p := range o.foldPending {
		if k2, id2, ok2 := dtdedup.Of(p.event); ok2 && k2 == kind && id2 == id {
			return p
		}
	}
	return nil
}

// foldTake removes and returns up to maxFoldPerTurn held completions for the
// running turn, dropping-and-acking stale-generation entries on the way
// (acceptance-and-drop, like the stale durable-timer drop). Lock held by
// caller (the turn).
func (o *orchestrator) foldTake(currentGen uint64) []*foldEntry {
	if len(o.foldPending) == 0 {
		return nil
	}

	kept := o.foldPending[:0]
	for _, p := range o.foldPending {
		if p.gen != currentGen {
			log.Infof("Workflow actor '%s': dropping held completion from previous generation %d (current %d)", o.actorID, p.gen, currentGen)
			close(p.committed)
			diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFolded)
			continue
		}
		kept = append(kept, p)
	}
	o.foldPending = kept

	n := len(o.foldPending)
	if n == 0 {
		return nil
	}
	if n > maxFoldPerTurn {
		n = maxFoldPerTurn
	}
	taken := o.foldPending[:n:n]
	o.foldPending = o.foldPending[n:]
	return taken
}

// foldedEvents projects entries onto their events, for the payload guard's
// merged-size accounting and for the held-event views.
func foldedEvents(entries []*foldEntry) []*backend.HistoryEvent {
	if len(entries) == 0 {
		return nil
	}
	events := make([]*backend.HistoryEvent, len(entries))
	for i, p := range entries {
		events[i] = p.event
	}
	return events
}

// foldExecutionMatches reports whether a completion's TaskExecutionId agrees
// with the scheduling event at its task id; empty ids on either side are
// tolerated (older SDKs, synthetic events).
func (o *orchestrator) foldExecutionMatches(e *backend.HistoryEvent, state *wfenginestate.State) bool {
	var taskID int32
	var execID string
	switch {
	case e.GetTaskCompleted() != nil:
		taskID = e.GetTaskCompleted().GetTaskScheduledId()
		execID = e.GetTaskCompleted().GetTaskExecutionId()
	case e.GetTaskFailed() != nil:
		taskID = e.GetTaskFailed().GetTaskScheduledId()
		execID = e.GetTaskFailed().GetTaskExecutionId()
	default:
		return true
	}
	if execID == "" {
		return true
	}
	scheduled := state.FindHistoryEventByID(taskID).GetTaskScheduled()
	if scheduled == nil {
		return false
	}
	if scheduled.GetTaskExecutionId() == "" {
		return true
	}
	return scheduled.GetTaskExecutionId() == execID
}

// foldAck signals the taken entries' senders that the commit containing
// their event succeeded, and records the folded outcome. The record lives
// here, on the commit side, not with a waiter: a sender whose invocation
// timed out before the folding turn committed is no longer waiting (its
// redelivery is absorbed as a duplicate), and a retry that joined a pending
// entry waits alongside the original, so waiter-side attribution both
// undercounts and overcounts. Every committed entry folds exactly once.
func foldAck(taken []*foldEntry) {
	for _, p := range taken {
		close(p.committed)
		diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFolded)
	}
}

// foldNack returns the taken entries' senders a recoverable error so their
// retry chains redeliver. Used on any turn outcome that did not commit the
// events (engine failure, CAN abandonment, stall, cancellation).
func foldNack(taken []*foldEntry, err error) {
	if len(taken) == 0 {
		return
	}
	if err == nil {
		err = errors.New("folding turn did not commit")
	}
	nerr := wferrors.NewRecoverable(fmt.Errorf("completion was not committed, redeliver: %w", err))
	for _, p := range taken {
		p.err = nerr
		close(p.committed)
		diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFoldNacked)
	}
}

// foldFlush nacks every held completion with a closed error; called from
// Deactivate so waiters unblock and senders retry against the next owner.
// Lock held by caller.
func (o *orchestrator) foldFlush() {
	if len(o.foldPending) == 0 {
		return
	}
	err := targeterrors.NewClosed("deactivated")
	for _, p := range o.foldPending {
		p.err = err
		close(p.committed)
		diag.DefaultWorkflowMonitoring.WorkflowCompletionsFold(context.Background(), diag.StatusFoldNacked)
	}
	o.foldPending = nil
}
