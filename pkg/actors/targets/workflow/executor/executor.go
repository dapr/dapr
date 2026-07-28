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
	// remains for waiters that fell back to a WatchComplete stream.
	if e.pending != nil && e.pending.Deliver(PendingKey(taskType, e.actorID), req.GetMessage().GetData().GetValue()) {
		// A stale watch stream from a superseded attempt may still be
		// parked on this actor; feed it a copy so it terminates promptly
		// instead of hanging until its context deadline. Duplicate
		// deliveries are discarded by the workflow-side dedup guards.
		select {
		case e.completeCh <- d:
		default:
		}
		e.tryDeactivate()
		return nil
	}

	// No waiter is registered on this host. If no watch stream is parked on
	// this actor either, and this call was not itself forwarded, forward once
	// to the sibling-format rendezvous actor: during a rolling upgrade the
	// waiter may rendezvous under the other version's activity key. Only
	// activity keys have sibling forms, so workflow completions never
	// forward (a workflow instance ID that happens to look like an activity
	// key must not be rewritten).
	if taskType == TaskTypeActivity && !isForwarded(req) && len(e.watchLock) == 0 {
		e.forwardSibling(ctx, req.GetMessage().GetData().GetValue())
	}

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

func (e *executor) cancel(req *internalsv1pb.InternalInvokeRequest) error {
	if e.pending != nil && e.pending.Cancel(PendingKey(taskTypeOf(req, e.actorID), e.actorID)) {
		e.tryDeactivate()
		return nil
	}

	// Cancels are at-least-once (stream disconnect cleanup and executor
	// shutdown can both cancel the same task); only the first closes.
	if e.cancelClosed.CompareAndSwap(false, true) {
		close(e.cancelCh)
	}
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

	close(e.closeCh)
	e.table.Delete(e.actorID)
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
