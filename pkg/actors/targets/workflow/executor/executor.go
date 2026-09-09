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

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc/codes"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	targeterrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.executor")

const (
	// MethodRegister arms the actor for the next work item; called before the
	// work item is dispatched so an early completion is parked for the
	// watcher and an unregistered one is dropped.
	MethodRegister      = "Register"
	MethodComplete      = "Complete"
	MethodCancel        = "Cancel"
	MethodWatchComplete = "WatchComplete"
)

// executor rendezvouses a work item's waiter with the completion the app
// reports, which may arrive on any daprd. Completions carry only the instance
// ID, so a waiter registers before its work item is dispatched: the
// registration discards any completion left behind by a superseded execution
// and the next completion is held for it. A watcher that attaches without
// registering (a daprd predating Register) takes whatever is parked, as
// before.
type executor struct {
	*factory
	actorID string

	// mu guards the fields below except watchLock and wg.
	mu sync.Mutex

	closed  bool
	closeCh chan struct{}

	// epoch changes on every Register so a superseded watcher does not tear
	// down the newer registration on exit.
	epoch     uint64
	armed     bool
	parked    *internalsv1pb.InternalInvokeResponse
	cancelled bool
	// watcher is the attached WatchComplete stream's delivery channel.
	watcher chan *internalsv1pb.InternalInvokeResponse

	watchLock chan struct{}
	wg        sync.WaitGroup
}

func (e *executor) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	e.wg.Add(1)
	defer e.wg.Done()

	switch req.GetMessage().GetMethod() {
	case MethodRegister:
		return nil, e.register()
	case MethodComplete:
		return nil, e.complete(req)
	case MethodCancel:
		return nil, e.cancel()
	default:
		return nil, errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) register() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return targeterrors.NewClosed("executor")
	}

	if e.parked != nil {
		log.Warnf("Executor actor '%s': discarding a parked completion that no waiter collected before a new registration", e.actorID)
		e.parked = nil
	}

	e.epoch++
	e.armed = true
	e.cancelled = false

	return nil
}

func (e *executor) complete(req *internalsv1pb.InternalInvokeRequest) error {
	d := &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{
			Code: int32(codes.OK),
		},
		Message: &commonv1pb.InvokeResponse{
			Data: req.GetMessage().GetData(),
		},
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return targeterrors.NewClosed("executor")
	}

	switch {
	case e.watcher != nil:
		select {
		case e.watcher <- d:
		default:
			log.Warnf("Executor actor '%s': dropping duplicate completion, the attached waiter already holds one", e.actorID)
		}
	case e.parked != nil:
		log.Warnf("Executor actor '%s': dropping duplicate completion, one is already parked", e.actorID)
	case e.armed:
		e.parked = d
	default:
		// Nothing registered: a superseded execution's completion, which the
		// next registration discards, or one for a watcher that does not
		// register, which takes it.
		log.Warnf("Executor actor '%s': parking completion with no registered waiter", e.actorID)
		e.parked = d
	}

	return nil
}

func (e *executor) cancel() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return targeterrors.NewClosed("executor")
	}

	if e.watcher != nil {
		select {
		case e.watcher <- abortedResponse():
		default:
		}
	} else {
		e.cancelled = true
	}

	return nil
}

func (e *executor) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("reminders are not implemented")
}

func (e *executor) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

// Deactivate closes the actor unconditionally (halt or rebalance); an attached
// watcher aborts its turn.
func (e *executor) Deactivate(_ context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.close()
	e.mu.Unlock()

	e.wg.Wait()
	return nil
}

// deactivateIfIdle closes the actor unless a waiter is armed or attached or a
// completion is parked. A caller that loaded the actor just before the close
// gets a closed error and is retried onto a fresh one by the router.
func (e *executor) deactivateIfIdle() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.armed || e.watcher != nil || e.parked != nil {
		return
	}
	e.close()
}

// close must be called with mu held.
func (e *executor) close() {
	e.closed = true
	close(e.closeCh)
	e.table.CompareAndDelete(e.actorID, e)
}

func (e *executor) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// tryDeactivate requests an idle deactivation without blocking; a full queue
// leaves the actor in the table until the next request or a halt.
func (e *executor) tryDeactivate() {
	select {
	case e.deactivateCh <- e:
	default:
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
		return e.watchComplete(ctx, stream)
	default:
		return errors.New("unknown method: " + req.GetMessage().GetMethod())
	}
}

func (e *executor) watchComplete(ctx context.Context, stream func(*internalsv1pb.InternalInvokeResponse) (bool, error)) error {
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

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return targeterrors.NewClosed("executor")
	}

	// Attaching arms the actor, so a watcher that never called Register works
	// as before.
	e.armed = true
	epoch := e.epoch

	var ch chan *internalsv1pb.InternalInvokeResponse
	defer func() {
		e.mu.Lock()
		if e.epoch == epoch {
			e.watcher = nil
			e.armed = false
			e.parked = nil
			e.cancelled = false
		} else if e.watcher == ch {
			// Superseded by a Register while still attached: a completion that
			// landed on this stream in the meantime belongs to the new
			// registration.
			e.watcher = nil
			select {
			case d := <-ch:
				if e.parked == nil {
					e.parked = d
				}
			default:
			}
		}
		e.mu.Unlock()
		e.tryDeactivate()
	}()

	if e.cancelled {
		e.cancelled = false
		e.mu.Unlock()
		_, err := stream(abortedResponse())
		return err
	}

	if d := e.parked; d != nil {
		e.parked = nil
		e.mu.Unlock()
		_, err := stream(d)
		return err
	}

	ch = make(chan *internalsv1pb.InternalInvokeResponse, 1)
	e.watcher = ch
	e.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-e.closeCh:
		// Only a halt closes an actor with an attached watcher; abort the turn
		// rather than retry against a moved actor.
		return backoff.Permanent(errors.New("closed"))
	case d := <-ch:
		_, err := stream(d)
		return err
	}
}

func abortedResponse() *internalsv1pb.InternalInvokeResponse {
	return &internalsv1pb.InternalInvokeResponse{
		Status: &internalsv1pb.Status{
			Code: int32(codes.Aborted),
		},
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
