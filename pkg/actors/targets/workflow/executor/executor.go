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
	"sync"
	"sync/atomic"

	"github.com/cenkalti/backoff/v4"
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

	// mu serializes each side's check-then-act pair of the rendezvous:
	// complete's pending-map miss followed by its channel park, cancel's map
	// miss followed by closing cancelCh, claim's drain of both, and close on
	// deactivation. Without it a waiter's whole Register+Claim can land
	// between a completer's miss and its park, with each side missing the
	// other. Every section held under it is non-blocking.
	mu sync.Mutex

	closed       atomic.Bool
	cancelClosed atomic.Bool
	wg           sync.WaitGroup
}

func (e *executor) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	e.wg.Add(1)
	defer e.wg.Done()

	switch req.GetMessage().GetMethod() {
	case MethodComplete:
		return nil, e.complete(ctx, req)
	case MethodCancel:
		return nil, e.cancel(req)
	case MethodClaim:
		return e.claim(req), nil
	default:
		return nil, errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) complete(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) error {
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

	// The waiter for this task normally lives on this host (it shares this
	// actor's ID, so placement co-locates them) and is registered in the
	// process-local pending map: deliver in-process. The channel park below
	// remains for waiters that fell back to a WatchComplete stream and for
	// waiters whose Register+Claim has not happened yet. The miss and the
	// park are one critical section: a claim can never observe the channel
	// empty after the map was checked but before the park lands.
	e.mu.Lock()
	if e.pending != nil && e.pending.Deliver(PendingKey(taskType, e.actorID), req.GetMessage().GetData().GetValue()) {
		// A stale watch stream from a superseded attempt may still be
		// parked on this actor; feed it a copy so it terminates promptly
		// instead of hanging until its context deadline. Duplicate
		// deliveries are discarded by the workflow-side dedup guards.
		select {
		case e.completeCh <- d:
		default:
		}
		e.mu.Unlock()
		e.tryDeactivate()
		return nil
	}

	// A concurrent claim may have found nothing and requested deactivation;
	// the close happens under mu, so this check is race-free: parking into a
	// closed actor would strand the payload, erroring instead makes the
	// caller's closed-actor retry redeliver onto a fresh actor.
	select {
	case <-e.closeCh:
		e.mu.Unlock()
		return targeterrors.NewClosed("executor")
	default:
	}

	parked := false
	select {
	case e.completeCh <- d:
		parked = true
	default:
	}
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
		e.forwardSibling(ctx, req.GetMessage().GetData().GetValue())
	}

	if parked {
		return nil
	}

	// The channel already holds an earlier parked completion (a superseded
	// attempt): block outside the critical section until a consumer or
	// lifecycle event resolves it.
	select {
	case e.completeCh <- d:
		return nil
	case <-e.cancelCh:
		return errors.New("canceled before completion result was sent")
	case <-e.closeCh:
		return targeterrors.NewClosed("executor")
	case <-ctx.Done():
		return errors.New("context cancelled before completion result was sent")
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

	select {
	case d := <-e.completeCh:
		// A parked completion of the colliding other task type (a workflow
		// instance ID equal to an activity actor ID) belongs to a different
		// waiter: put it back for its own watcher or claimer, and keep the
		// actor alive for it.
		if v, ok := d.GetHeaders()[MetadataTaskType]; ok && len(v.GetValues()) > 0 && v.GetValues()[0] != claimType {
			select {
			case e.completeCh <- d:
			default:
			}
			return &internalsv1pb.InternalInvokeResponse{
				Status: &internalsv1pb.Status{Code: int32(codes.NotFound)},
			}
		}

		// A stale watch stream from a superseded attempt may still be parked
		// on this actor; feed it a copy so it terminates promptly. Duplicate
		// deliveries are discarded by the workflow-side dedup guards.
		if len(e.watchLock) > 0 {
			select {
			case e.completeCh <- d:
			default:
			}
		} else {
			e.tryDeactivate()
		}
		return d
	default:
	}

	select {
	case <-e.cancelCh:
		e.tryDeactivate()
		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{Code: int32(codes.Aborted)},
		}
	default:
	}

	// Nothing parked: the waiter rendezvouses through the pending map, which
	// completions arriving on this daprd reach without touching this actor,
	// so don't leave an idle entry in the table (a later remote forward
	// simply re-creates it). A completion racing this deactivation either
	// finds the still-registered waiter in the map, or observes the closed
	// actor (the close and complete's closed-check are both under mu) and is
	// redelivered by the caller's retry onto a fresh actor. A park can only
	// slip in before the close after the waiter deregistered, where the
	// durable reminder retry already owns redelivery.
	e.tryDeactivate()
	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{Code: int32(codes.NotFound)},
	}
}

func (e *executor) cancel(req *internalsv1pb.InternalInvokeRequest) error {
	e.mu.Lock()
	if e.pending != nil && e.pending.Cancel(PendingKey(taskTypeOf(req, e.actorID), e.actorID)) {
		e.mu.Unlock()
		e.tryDeactivate()
		return nil
	}

	// Cancels are at-least-once (stream disconnect cleanup and executor
	// shutdown can both cancel the same task); only the first closes. The
	// miss and the close are one mu critical section, mirroring complete's
	// miss-then-park, so a claim can never run between them.
	if e.cancelClosed.CompareAndSwap(false, true) {
		close(e.cancelCh)
	}
	e.mu.Unlock()
	return nil
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

func (e *executor) Deactivate(_ context.Context) error {
	if !e.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Close under mu so complete's closed-check-then-park cannot straddle
	// the close and strand a payload in a deactivated actor. wg.Wait stays
	// outside: in-flight invocations hold wg and may be waiting on mu.
	e.mu.Lock()
	close(e.closeCh)
	e.table.Delete(e.actorID)
	e.mu.Unlock()
	e.wg.Wait()
	return nil
}

func (e *executor) InvokeStream(ctx context.Context,
	req *internalsv1pb.InternalInvokeRequest,
	stream func(*internalsv1pb.InternalInvokeResponse) (bool, error),
) error {
	e.wg.Add(1)
	defer e.wg.Done()

	switch req.GetMessage().GetMethod() {
	case MethodWatchComplete:
		return e.watchComplete(ctx, stream)
	default:
		return errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) watchComplete(ctx context.Context, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
	defer func() {
		e.deactivateCh <- e
	}()

	select {
	case e.watchLock <- struct{}{}:
	case <-e.closeCh:
		return backoff.Permanent(errors.New("closed"))
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		<-e.watchLock
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.closeCh:
		return backoff.Permanent(errors.New("closed"))
	case <-e.cancelCh:
		_, err := stream(&internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
				Code: int32(codes.Aborted),
			},
		})
		return err
	case d := <-e.completeCh:
		_, err := stream(d)
		return err
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
