/*
Copyright 2025 The Dapr Authors
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

package executor

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	"github.com/dapr/dapr/pkg/actors/targets/workflow/common/lock"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.executor")

const (
	MethodComplete      = "Complete"
	MethodCancel        = "Cancel"
	MethodClaim         = "Claim"
	MethodWatchComplete = "WatchComplete"
)

type executor struct {
	*factory
	actorID string
	lock    *lock.Lock

	closeCh    chan struct{}
	completeCh chan *internalsv1pb.InternalInvokeResponse
	cancelCh   chan struct{}

	watchLock chan struct{}

	// displaced holds a parked completion of the colliding other task type
	// that a claim drained from completeCh. It cannot be put back on the
	// channel: a completer blocked on a full channel (the only sender
	// outside mu) can refill the freed slot first, and the non-blocking
	// put-back would silently drop the payload. Guarded by mu; consumed by
	// the first matching claim or watch stream. While it is non-nil the
	// actor must not deactivate, or the payload would be stranded.
	displaced *internalsv1pb.InternalInvokeResponse

	// mu serializes each side's check-then-act pair of the rendezvous:
	// complete's pending-map miss followed by its channel park, cancel's map
	// miss followed by closing cancelCh, claim's drain of both, and close on
	// deactivation. Without it a waiter's whole Register+Claim can land
	// between a completer's miss and its park, with each side missing the
	// other. Every section held under it is non-blocking.
	mu sync.Mutex

	// slotFreed is closed and replaced, under mu, after every receive that
	// frees the completeCh slot. A completer that found the channel full
	// waits on it and then repeats its check and park under mu.
	slotFreed chan struct{}

	closed       atomic.Bool
	cancelClosed atomic.Bool
	// cancelType is the task type of a recorded cancellation that nothing
	// served yet, so a retiring actor can hand it off with its type, and
	// cancelHandoffs is its handoff count. Guarded by mu.
	cancelType     string
	cancelHandoffs int
	wg             sync.WaitGroup
}

func (e *executor) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	e.wg.Add(1)

	var res *internalsv1pb.InternalInvokeResponse
	var run func()
	var err error

	defer func() {
		if run != nil {
			run()
		}
	}()
	defer e.wg.Done()

	switch req.GetMessage().GetMethod() {
	case MethodComplete:
		run, err = e.complete(ctx, req)
	case MethodCancel:
		run, err = e.cancel(req)
	case MethodClaim:
		res = e.claim(req)
	default:
		err = errors.New("unknown method: " + req.GetMessage().GetMethod())
	}

	return res, err
}

func (e *executor) complete(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (func(), error) {
	taskType := taskTypeOf(req, e.actorID)

	// The task type header lets a watcher of the colliding other task type
	// (a workflow instance ID equal to an activity actor ID) reject a
	// completion that is not for it; pre-upgrade watchers ignore it.
	d := &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{
			Code: int32(codes.OK),
		},
		Headers: map[string]*internalsv1pb.ListStringValue{
			MetadataTaskType: {Values: []string{taskType}},
		},
		Message: &commonv1pb.InvokeResponse{
			Data: req.GetMessage().GetData(),
		},
	}
	// A forwarded completion keeps its marker and its handoff count while
	// parked, so a retiring actor knows whether it may move it again.
	if isForwarded(req) {
		d.Headers[MetadataForwarded] = &internalsv1pb.ListStringValue{Values: []string{forwardedValue}}
	}
	if n, ok := handoffsIn(req.GetMetadata()); ok {
		d.Headers[MetadataHandoffs] = &internalsv1pb.ListStringValue{Values: []string{strconv.Itoa(n)}}
	}
	data := req.GetMessage().GetData().GetValue()

	// The waiter for this task normally lives on this host (it shares this
	// actor's ID, so placement co-locates them) and is registered in the
	// process-local pending map: deliver in-process. A callback waiter's
	// continuation is handed back as run and executed by InvokeMethod on
	// this goroutine once every lock is released. The channel park below
	// remains for waiters that fell back to a WatchComplete stream and for
	// waiters whose Register+Claim has not happened yet. The miss and the
	// park are one critical section: a claim can never observe the channel
	// empty after the map was checked but before the park lands.
	e.mu.Lock()
	if run, deactivate, ok := e.deliverLocked(taskType, d, data); ok {
		e.mu.Unlock()
		if deactivate {
			e.tryDeactivate()
		}
		return run, nil
	}

	// A concurrent claim may have found nothing and requested deactivation;
	// the close happens under mu, so this check is race-free: parking into a
	// closed actor would strand the payload, erroring instead makes the
	// caller's closed-actor retry redeliver onto a fresh actor.
	if e.closedLocked() {
		e.mu.Unlock()
		return nil, targeterrors.NewClosed("executor")
	}

	parked := e.parkLocked(d)
	freed := e.slotFreed
	forward := taskType == TaskTypeActivity && !isForwarded(req) && len(e.watchLock) == 0
	e.mu.Unlock()

	// No waiter is registered on this host. If no watch stream is parked on
	// this actor either, and this call was not itself forwarded, forward once
	// to the sibling-format rendezvous actor: during a rolling upgrade the
	// waiter may rendezvous under the other version's activity key. Only
	// activity keys have sibling forms, so workflow completions never
	// forward (a workflow instance ID that happens to look like an activity
	// key must not be rewritten).
	if forward {
		e.forwardSibling(ctx, data)
	}

	if parked {
		return nil, nil
	}

	// The channel already holds an earlier parked completion (a superseded
	// attempt). Wait until a receive frees the slot, then repeat the
	// pending-map check and the park under mu. Every park then follows a
	// map miss in the same critical section: a waiter that registered in
	// the meantime gets the payload directly, and nothing can park after a
	// retirement drained the actor under mu.
	for {
		select {
		case <-freed:
		case <-e.cancelCh:
			return nil, errors.New("canceled before completion result was sent")
		case <-e.closeCh:
			return nil, targeterrors.NewClosed("executor")
		case <-ctx.Done():
			return nil, errors.New("context cancelled before completion result was sent")
		}

		e.mu.Lock()
		if run, deactivate, ok := e.deliverLocked(taskType, d, data); ok {
			e.mu.Unlock()
			if deactivate {
				e.tryDeactivate()
			}
			return run, nil
		}
		if e.closedLocked() {
			e.mu.Unlock()
			return nil, targeterrors.NewClosed("executor")
		}
		if e.parkLocked(d) {
			e.mu.Unlock()
			return nil, nil
		}
		freed = e.slotFreed
		e.mu.Unlock()
	}
}

// deliverLocked hands data to a waiter registered in the pending map, and
// reports whether it did and whether the actor may deactivate. A stale watch
// stream from a superseded attempt may still be parked on this actor, so a
// delivery also parks a copy that it can take and end on; the workflow-side
// dedup guards discard the duplicate. The copy carries the forwarded marker
// without a handoff count, so a retiring actor never hands it to another
// waiter. Must be called with mu held.
func (e *executor) deliverLocked(taskType string, d *internalsv1pb.InternalInvokeResponse, data []byte) (func(), bool, bool) {
	if e.pending == nil {
		return nil, false, false
	}
	run, delivered := e.pending.DeliverDeferred(PendingKey(taskType, e.actorID), data)
	if !delivered {
		return nil, false, false
	}

	delete(d.GetHeaders(), MetadataHandoffs)
	d.Headers[MetadataForwarded] = &internalsv1pb.ListStringValue{Values: []string{forwardedValue}}
	e.parkLocked(d)
	return run, e.displaced == nil, true
}

// parkLocked parks d if the completeCh slot is free. Must be called with mu
// held: parks happen only under mu.
func (e *executor) parkLocked(d *internalsv1pb.InternalInvokeResponse) bool {
	select {
	case e.completeCh <- d:
		return true
	default:
		return false
	}
}

// slotFreedLocked wakes the completers that wait for a free completeCh slot.
// Must be called with mu held, after a receive from completeCh.
func (e *executor) slotFreedLocked() {
	close(e.slotFreed)
	e.slotFreed = make(chan struct{})
}

// closedLocked reports whether the actor has closed. Must be called with mu
// held, where the close also happens.
func (e *executor) closedLocked() bool {
	select {
	case <-e.closeCh:
		return true
	default:
		return false
	}
}

// claim hands a parked completion to a co-located waiter whose pending-map
// registration lost the race with the completion RPC: complete() found no
// waiter and parked the payload in completeCh, which the pending map never
// consults. The waiter registers first and claims second; complete()'s
// map-miss and park form one mu critical section and this whole drain is
// another, so the two sides cannot interleave: a completer that missed the
// map has parked before any later claim runs, and a claim that found nothing
// ran before the completer's map check, which then finds the registered
// waiter. Non-blocking: no parked completion means the waiter goes back to
// its pending-map channel, where any later completion is delivered directly.
func (e *executor) claim(req *internalsv1pb.InternalInvokeRequest) *internalsv1pb.InternalInvokeResponse {
	claimType := taskTypeOf(req, e.actorID)

	e.mu.Lock()
	defer e.mu.Unlock()

	// A completion of this type displaced by an earlier claim of the
	// colliding other task type is checked first: it is older than anything
	// still in the channel.
	if d := e.displaced; d != nil && parkedTaskType(d) == claimType {
		e.displaced = nil
		return e.claimed(d)
	}

	// Drain parked completions. One of the colliding other task type (a
	// workflow instance ID equal to an activity actor ID) belongs to a
	// different waiter and is moved to the displaced slot rather than put
	// back: a waiting completer can take the slot the drain freed. A
	// same-type duplicate overwrites the slot, which the workflow-side dedup
	// guards make safe.
	for {
		var d *internalsv1pb.InternalInvokeResponse
		select {
		case d = <-e.completeCh:
			e.slotFreedLocked()
		default:
		}
		if d == nil {
			break
		}
		if pt := parkedTaskType(d); pt != "" && pt != claimType {
			e.displaced = d
			continue
		}
		return e.claimed(d)
	}

	select {
	case <-e.cancelCh:
		// Served: a retiring actor must not hand it to a later attempt's
		// waiter. cancelCh stays closed for later claims on this actor.
		e.cancelType = ""
		if e.displaced == nil {
			e.tryDeactivate()
		}
		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{Code: int32(codes.Aborted)},
		}
	default:
	}

	// Nothing parked for this type: the waiter rendezvouses through the
	// pending map, which completions arriving on this daprd reach without
	// touching this actor, so don't leave an idle entry in the table (a
	// later remote forward simply re-creates it), unless a displaced
	// completion still needs the actor alive for its own waiter. A
	// completion racing this deactivation either finds the still-registered
	// waiter in the map, or observes the closed actor (the close and
	// complete's closed-check are both under mu) and is redelivered by the
	// caller's retry onto a fresh actor. A park can only slip in before the
	// close after the waiter deregistered, where the durable reminder retry
	// already owns redelivery.
	if e.displaced == nil {
		e.tryDeactivate()
	}
	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{Code: int32(codes.NotFound)},
	}
}

// claimed finalizes a successful claim under mu: a stale watch stream from a
// superseded attempt is fed a copy so it terminates promptly (duplicates are
// discarded by the workflow-side dedup guards), and the actor deactivates
// when no watcher is parked and nothing displaced remains for the colliding
// other task type.
func (e *executor) claimed(d *internalsv1pb.InternalInvokeResponse) *internalsv1pb.InternalInvokeResponse {
	if len(e.watchLock) > 0 {
		e.parkLocked(d)
	} else if e.displaced == nil {
		e.tryDeactivate()
	}
	return d
}

// parkedTaskType reads the task type a parked completion was stamped with by
// complete; "" only for payloads parked by builds predating the stamp, which
// any claimer may take.
func parkedTaskType(d *internalsv1pb.InternalInvokeResponse) string {
	if v, ok := d.GetHeaders()[MetadataTaskType]; ok && len(v.GetValues()) > 0 {
		return v.GetValues()[0]
	}
	return ""
}

func (e *executor) cancel(req *internalsv1pb.InternalInvokeRequest) (func(), error) {
	e.mu.Lock()
	var run func()
	var cancelled bool
	if e.pending != nil {
		run, cancelled = e.pending.CancelDeferred(PendingKey(taskTypeOf(req, e.actorID), e.actorID))
	}
	if cancelled {
		deactivate := e.displaced == nil
		e.mu.Unlock()
		if deactivate {
			e.tryDeactivate()
		}
		return run, nil
	}

	// Cancels are at-least-once (stream disconnect cleanup and executor
	// shutdown can both cancel the same task); only the first closes. The
	// miss and the close are one mu critical section, mirroring complete's
	// miss-then-park, so a claim can never run between them.
	if e.cancelClosed.CompareAndSwap(false, true) {
		// Record it so a retiring actor can hand it off, unless it arrived
		// by a forward that carries no handoff count (it never moves again).
		if n, ok := handoffsIn(req.GetMetadata()); ok {
			e.cancelType, e.cancelHandoffs = taskTypeOf(req, e.actorID), n
		} else if !isForwarded(req) {
			e.cancelType, e.cancelHandoffs = taskTypeOf(req, e.actorID), 0
		}
		close(e.cancelCh)
	}
	e.mu.Unlock()
	return nil, nil
}

// tryDeactivate requests deactivation without ever blocking the caller: the
// deactivation queue is drained serially and each item waits on the actor's
// wait group, which the caller is currently holding. If the queue is full the
// actor simply stays in the table until HaltNonHosted or shutdown reaps it.
func (e *executor) tryDeactivate() {
	select {
	case e.deactivateCh <- e:
	default:
	}
}

func (e *executor) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("reminders are not implemented")
}

func (e *executor) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

// Deactivate retires an idle actor. What it still holds goes to the key's
// owner, which is this host, so it lands on a fresh actor.
func (e *executor) Deactivate(ctx context.Context) error {
	e.retire(ctx)
	return nil
}

// halt retires an actor that this host no longer serves: placement moved its
// key, or the type left this host. A completion or cancellation still parked
// on it was waiting for a watcher that has not attached yet, often one whose
// stream the rebalance drain cancelled and that is retrying. That watcher
// attaches on the key's new owner, so the parked result goes there.
func (e *executor) halt(ctx context.Context) {
	e.retire(ctx)
}

// retire closes the actor and hands off what was still parked on it. Every
// retirement does: a completion that this actor accepted must not be dropped
// with it.
func (e *executor) retire(ctx context.Context) {
	parked, canc := e.deactivate()
	if len(parked) > 0 || canc.taskType != "" {
		e.handOff(ctx, parked, canc)
	}
}

// deactivate closes the actor and returns what was still parked on it: the
// completions in the channel and the displaced slot, and the recorded
// cancellation that nothing served.
func (e *executor) deactivate() ([]*internalsv1pb.InternalInvokeResponse, parkedCancel) {
	if !e.closed.CompareAndSwap(false, true) {
		return nil, parkedCancel{}
	}

	// Close under mu, where every park happens, so nothing can park after
	// the drain. wg.Wait stays outside: in-flight invocations hold wg and
	// may be waiting on mu.
	e.mu.Lock()
	close(e.closeCh)
	e.table.Delete(e.actorID)
	var parked []*internalsv1pb.InternalInvokeResponse
	if e.displaced != nil {
		parked = append(parked, e.displaced)
		e.displaced = nil
	}
	parked = e.drainParked(parked)
	canc := parkedCancel{taskType: e.cancelType, handoffs: e.cancelHandoffs}
	e.mu.Unlock()
	e.wg.Wait()

	return parked, canc
}

func (e *executor) drainParked(parked []*internalsv1pb.InternalInvokeResponse) []*internalsv1pb.InternalInvokeResponse {
	for {
		select {
		case d := <-e.completeCh:
			parked = append(parked, d)
		default:
			return parked
		}
	}
}

func (e *executor) InvokeStream(ctx context.Context,
	req *internalsv1pb.InternalInvokeRequest,
	stream func(*internalsv1pb.InternalInvokeResponse) (bool, error),
) error {
	e.wg.Add(1)
	defer e.wg.Done()

	switch req.GetMessage().GetMethod() {
	case MethodWatchComplete:
		return e.watchComplete(ctx, req, stream)
	default:
		return errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) watchComplete(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	defer func() {
		// A displaced completion still needs the actor alive for its own
		// waiter; skip deactivation until it is consumed. Anything else
		// parked here moves to a fresh actor when this one retires.
		// Non-blocking: a blocking send can deadlock when the queue is full
		// and its consumer is waiting on this actor's wait group, which this
		// stream holds.
		e.mu.Lock()
		displaced := e.displaced != nil
		e.mu.Unlock()
		if !displaced {
			e.tryDeactivate()
		}
	}()

	select {
	case e.watchLock <- struct{}{}:
	case <-e.closeCh:
		return targeterrors.NewClosed("executor")
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		<-e.watchLock
	}()

	// A watcher advertising no type (a pre-upgrade daprd) takes whatever is
	// held and applies its own type check stream-side.
	watchType := ""
	if v, ok := req.GetMetadata()[MetadataTaskType]; ok && len(v.GetValues()) > 0 {
		watchType = v.GetValues()[0]
	}
	forWatcher := func(d *internalsv1pb.InternalInvokeResponse) bool {
		pt := parkedTaskType(d)
		return watchType == "" || pt == "" || pt == watchType
	}

	for {
		// A completion displaced by a wrong-type claim never reaches the
		// channel select below: hand it to the first watcher whose type
		// matches.
		e.mu.Lock()
		if d := e.displaced; d != nil && forWatcher(d) {
			e.displaced = nil
			e.mu.Unlock()
			_, err := stream(d)
			return err
		}
		e.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.closeCh:
			return targeterrors.NewClosed("executor")
		case <-e.cancelCh:
			// Served: a retiring actor must not hand it to a later
			// attempt's waiter.
			e.mu.Lock()
			e.cancelType = ""
			e.mu.Unlock()
			_, err := stream(&internalsv1pb.InternalInvokeResponse{
				Status: &internalsv1pb.Status{
					Code: int32(codes.Aborted),
				},
			})
			return err
		case d := <-e.completeCh:
			e.mu.Lock()
			e.slotFreedLocked()
			if !forWatcher(d) {
				// The colliding other task type's completion (a workflow
				// instance ID equal to an activity actor ID) belongs to a
				// different waiter: keep it in the displaced slot, as
				// claim does, and wait for this watcher's own. A claim of
				// the other type can have displaced one for this watcher
				// while it waited: serve that one first.
				mine := e.displaced
				e.displaced = d
				e.mu.Unlock()
				if mine != nil && forWatcher(mine) {
					_, err := stream(mine)
					return err
				}
				continue
			}
			e.mu.Unlock()
			_, err := stream(d)
			return err
		}
	}
}

func (e *executor) Key() string {
	return e.actorType + actorapi.DaprSeparator + e.actorID
}

func (e *executor) Type() string {
	return e.actorType
}

func (e *executor) ID() string {
	return e.actorID
}
