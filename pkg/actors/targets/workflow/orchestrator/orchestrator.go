/*
Copyright 2024 The Dapr Authors
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
	"sync"
	"sync/atomic"
	"time"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/messages"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/signing"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.orchestrator")

type EventSink func(*backend.WorkflowMetadata)

type orchestrator struct {
	*factory

	actorID string

	state  *wfenginestate.State
	rstate *backend.WorkflowRuntimeState
	ometa  *backend.WorkflowMetadata

	activityResultAwaited atomic.Bool
	// janitorAsserted tracks whether the per-instance janitor backstop
	// reminder was ensured this actor residency (WorkflowsFastPath).
	janitorAsserted atomic.Bool
	// Drive-loop state (see wake.go localDrive/driveLoop): driveNotify is a
	// buffered-1 coalescing notification channel, driveRunning guards the
	// single loop, driveInfo carries the latest wake identity for the loop
	// and its escalation.
	driveNotify  chan struct{}
	driveRunning atomic.Bool

	// lastActive is the UnixNano of the most recent turn-lock acquisition,
	// stamped for the factory idle reaper. Also stamped at creation so a fresh
	// actor is never reaped before its first turn.
	lastActive atomic.Int64
	// inTurn counts currently-held turn locks (0 or 1 plus queued stream
	// claims): the reaper must never deactivate an actor mid-turn, and
	// lastActive alone cannot show that once a turn (an app roundtrip of
	// arbitrary length) outlives the idle TTL.
	inTurn atomic.Int32
	// lastProgress is the UnixNano of the most recent durable state commit
	// (stamped in signAndSaveState). Zero on a fresh activation, so an actor
	// recreated after a crash reads as stalled and the durable backstops
	// recover it. INVARIANT: never stamped by the janitor fire or by lock
	// traffic; it distinguishes "alive and progressing" from "being polled".
	lastProgress atomic.Int64
	driveInfo    atomic.Pointer[driveInfo]
	// wakeEpoch counts durable turn commits; escalate suppresses wakes armed
	// before the latest commit (their durable reminder would be a stray).
	// Bumped only at the turn-commit sites in runWorkflow; never reset
	// (monotonic avoids ABA with a pre-halt escalation).
	wakeEpoch atomic.Uint64
	// foldPending holds sender-retried completions awaiting their folding
	// turn (WorkflowsFastPath; see fold.go). INVARIANT: only touched
	// while holding the per-actor turn lock (submit, turn, janitor,
	// Deactivate all hold it); waiters read their own done channel lock-free.
	foldPending []*foldEntry
	// janitorIdleFires counts consecutive no-op janitor fires, driving the
	// exponential backoff of the stale-cache store probe in runJanitor.
	// Reset by any recovery action or a normal turn. Guarded by the actor
	// turn lock like every janitor field.
	janitorIdleFires int

	// janitorRedispatchedGen is the state generation janitorRedispatched
	// was built against: a ContinueAsNew generation reuses task IDs from
	// zero, so a stale map would skip a new task's first local re-dispatch.
	janitorRedispatchedGen uint64

	// janitorRedispatched records the task IDs of TaskScheduled events the
	// janitor re-dispatched this residency, so a task still unresolved at the
	// NEXT fire escalates to the durable run-activity reminder instead of
	// re-arming the unverifiable local drive (see redispatchActivities).
	// INVARIANT: only touched by janitor fires, which hold the turn lock.
	janitorRedispatched map[int32]struct{}

	// janitorEscalated records the task IDs whose re-dispatch escalated to
	// the durable run-activity reminder this residency, so the turn that
	// commits the task's completion can reap the reminder (the completion's
	// own execution never knew it existed; see reapEscalatedCompletions).
	// Same guard and generation scope as janitorRedispatched.
	janitorEscalated map[int32]struct{}
	lock             *lock.Stallable
	closed           atomic.Bool
	wg               sync.WaitGroup

	streamFns map[int64]*streamFn
	streamIDx int64

	signing  *signing.Signing
	messages *messages.Messages
}

type streamFn struct {
	fn    func(*internalsv1pb.InternalInvokeResponse) (bool, error)
	errCh chan error
	done  atomic.Bool
}

// InvokeMethod implements actors.InternalActor
func (o *orchestrator) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	o.wg.Add(1)
	defer o.wg.Done()

	// The completions fold manages its own lock lifecycle (early release
	// before waiting on the folding turn's commit; see fold.go).
	if o.fastPath && req.GetMessage().GetMethod() == todo.AddWorkflowEventMethod {
		return o.invokeAddEventFold(ctx, req)
	}

	unlock, err := o.contextLockMeasured(ctx, "method")
	if err != nil {
		return nil, fmt.Errorf("failed to invoke method for workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	return o.handleInvoke(ctx, req)
}

// InvokeReminder implements actors.InternalActor
func (o *orchestrator) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	o.wg.Add(1)
	defer o.wg.Done()

	unlock, err := o.contextLockMeasured(ctx, "reminder")
	if err != nil {
		return fmt.Errorf("failed to invoke reminder for workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	return o.handleReminder(ctx, reminder)
}

// contextLockMeasured acquires the per-actor turn lock and records the wait
// as the lock_wait histogram (sampled 1-in-16), splitting observed
// invocation latency into queueing vs turn body.
func (o *orchestrator) contextLockMeasured(ctx context.Context, kind string) (context.CancelFunc, error) {
	if o.lockWaitSample.Add(1)%16 != 0 {
		unlock, err := o.lock.ContextLock(ctx)
		if err != nil {
			return unlock, err
		}
		return o.turnHeld(unlock), nil
	}
	start := time.Now()
	unlock, err := o.lock.ContextLock(ctx)
	elapsed := float64(time.Since(start)) / float64(time.Millisecond)
	if err != nil {
		return unlock, err
	}
	diag.DefaultWorkflowMonitoring.WorkflowLockWait(ctx, kind, elapsed)
	return o.turnHeld(unlock), nil
}

func (o *orchestrator) turnHeld(unlock context.CancelFunc) context.CancelFunc {
	o.lastActive.Store(time.Now().UnixNano())
	o.inTurn.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			o.lastActive.Store(time.Now().UnixNano())
			o.inTurn.Add(-1)
		})
		unlock()
	}
}

// InvokeTimer implements actors.InternalActor
func (o *orchestrator) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

func (o *orchestrator) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	o.wg.Add(1)
	defer o.wg.Done()

	unlock, err := o.contextLockMeasured(ctx, "stream")
	if err != nil {
		return fmt.Errorf("failed to invoke reminder for workflow '%s': %w", o.actorID, err)
	}

	var ok bool
	ok, err = o.handleStream(ctx, req, stream, unlock)
	if !ok {
		unlock()
	}
	return err
}

// DeactivateActor implements actors.InternalActor
func (o *orchestrator) Deactivate(ctx context.Context) error {
	unlock, err := o.lock.ContextLock(ctx)
	if targeterrors.IsStalled(err) {
		// The actor is parked in the stall hold; wake it so its invocation
		// returns and releases the lock, then take the lock to deactivate.
		o.lock.ReleaseStall()
		unlock, err = o.lock.ContextLock(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to deactivate workflow '%s': %w", o.actorID, err)
	}
	defer unlock()

	o.table.Delete(o.actorID)
	o.invalidateCachedState()
	o.lock.Close()
	o.foldFlush()
	for _, stream := range o.streamFns {
		stream.errCh <- targeterrors.NewClosed("deactivated")
	}
	clear(o.streamFns)
	o.signing.Reset()
	o.wg.Wait()

	return nil
}

// Key returns the key for this unique actor.
func (o *orchestrator) Key() string {
	return o.actorType + actorapi.DaprSeparator + o.actorID
}

// Type returns the type for this unique actor.
func (o *orchestrator) Type() string {
	return o.actorType
}

// ID returns the ID for this unique actor.
func (o *orchestrator) ID() string {
	return o.actorID
}
