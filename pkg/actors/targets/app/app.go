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

package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/api"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/internal/key"
	"github.com/dapr/dapr/pkg/actors/targets/app/lock"
	targetserrors "github.com/dapr/dapr/pkg/actors/targets/errors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.app")

type app struct {
	*factory

	actorID string

	// idleAt is the time after which this actor is considered to be idle.
	// When the actor is locked, idleAt is updated by adding the idleTimeout to
	// the current time.
	idleAt atomic.Pointer[time.Time]

	lock  *lock.Lock
	clock clock.Clock

	closed atomic.Bool

	endEvictHook func()

	evictLock  sync.Mutex
	evicted    bool
	evictCalls int

	idleLock sync.Mutex
	evicting bool
}

func (a *app) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	ctx, cancel, err := a.lock.LockRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cancel()

	a.touchIdle()
	release, err := a.evictStale(ctx)
	defer release()
	if err != nil {
		return nil, err
	}
	return a.transport.Invoke(ctx, req)
}

func (a *app) InvokeReminder(ctx context.Context, reminder *api.Reminder) error {
	// Build a minimal request for the reentrancy lock keyed on
	// "remind/<name>". Reminder names may contain '/' (e.g. workflow-style
	// keys), so the name is passed through verbatim here — path-traversal
	// sanitization happens in the HTTP transport where the method is
	// embedded in a URL path.
	lockReq := internalv1pb.NewInternalInvokeRequest("remind/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID)

	ctx, cancel, err := a.lock.LockRequest(ctx, lockReq)
	if err != nil {
		return err
	}
	defer cancel()

	a.touchIdle()
	release, err := a.evictStale(ctx)
	defer release()
	if err != nil {
		return err
	}
	log.Debug("Executing reminder for actor " + reminder.Key())

	if err := a.transport.InvokeReminder(ctx, reminder, lockReq.GetMetadata()); err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing reminder for actor %s: %v", reminder.Key(), err)
		}
		return err
	}
	return nil
}

func (a *app) InvokeTimer(ctx context.Context, reminder *api.Reminder) error {
	// See InvokeReminder for the path-sanitization split between the app
	// layer and the HTTP transport.
	lockReq := internalv1pb.NewInternalInvokeRequest("timer/"+reminder.Name).
		WithActor(reminder.ActorType, reminder.ActorID)

	ctx, cancel, err := a.lock.LockRequest(ctx, lockReq)
	if err != nil {
		return err
	}
	defer cancel()

	a.touchIdle()
	release, err := a.evictStale(ctx)
	defer release()
	if err != nil {
		return err
	}
	log.Debug("Executing timer for actor " + reminder.Key())

	if err := a.transport.InvokeTimer(ctx, reminder, lockReq.GetMetadata()); err != nil {
		if !errors.Is(err, actorerrors.ErrReminderCanceled) {
			log.Errorf("Error executing timer for actor %s: %v", reminder.Key(), err)
		}
		return err
	}
	return nil
}

func (a *app) Deactivate(ctx context.Context) error {
	if !a.closeLock(ctx, false) {
		return nil
	}
	return a.teardown()
}

func (a *app) closeLock(ctx context.Context, idle bool) bool {
	a.idleLock.Lock()
	defer a.idleLock.Unlock()
	if idle && a.evicting {
		log.Debugf("Actor %s is mid-eviction, deferring idle deactivation", a.Key())
		return false
	}
	if !a.closed.CompareAndSwap(false, true) {
		return false
	}
	a.lock.Close(ctx)
	return true
}

func (a *app) teardown() error {
	a.table.Delete(a.actorID)

	if err := a.transport.Deactivate(context.Background(), a.actorType, a.actorID); err != nil {
		return err
	}

	a.idlerQueue.Dequeue(key.ConstructComposite(a.actorType, a.actorID))
	diag.DefaultMonitoring.ActorDeactivated(a.actorType)
	log.Debugf("Deactivated actor '%s'", a.Key())
	return nil
}

// Key returns the key for this unique actor.
func (a *app) Key() string {
	return a.actorType + api.DaprSeparator + a.actorID
}

// Type returns the actor type.
func (a *app) Type() string {
	return a.actorType
}

func (a *app) ID() string {
	return a.actorID
}

// ScheduledTime returns the time the actor becomes idle at.
// This is implemented to comply with the queueable interface.
func (a *app) ScheduledTime() time.Time {
	return *a.idleAt.Load()
}

func (a *app) InvokeStream(context.Context,
	*internalv1pb.InternalInvokeRequest,
	func(*internalv1pb.InternalInvokeResponse) (bool, error),
) error {
	return errors.New("not implemented")
}

func (a *app) evictStale(ctx context.Context) (func(), error) {
	a.evictLock.Lock()
	defer a.evictLock.Unlock()

	a.evictCalls++
	if a.evictCalls == 1 && !a.evicted && a.startEvict(ctx) {
		func() {
			defer func() {
				a.endEvict()
				if r := recover(); r != nil {
					a.evictCalls--
					panic(r)
				}
			}()
			a.evicted = a.evict(ctx)
		}()
	}

	release := func() {
		a.evictLock.Lock()
		defer a.evictLock.Unlock()
		a.evictCalls--
	}

	if cause := context.Cause(ctx); targetserrors.IsClosed(cause) {
		return release, cause
	}

	return release, nil
}

func (a *app) evict(ctx context.Context) bool {
	ectx, cancel := context.WithTimeout(ctx, a.evictTimeout)
	defer cancel()

	err := a.transport.Evict(ectx, a.actorType, a.actorID)
	switch {
	case err == nil:
	case ctx.Err() == nil && errors.Is(ectx.Err(), context.DeadlineExceeded):
		log.Warnf("App did not answer eviction of actor '%s' within %s, proceeding with the call", a.Key(), a.evictTimeout)
	default:
		log.Debugf("Failed to evict stale app instance of actor '%s', retrying on the next call: %v", a.Key(), err)
		return false
	}

	return true
}

func (a *app) startEvict(ctx context.Context) bool {
	a.idleLock.Lock()
	defer a.idleLock.Unlock()
	// Every lock close runs under idleLock, so a live ctx means no close has started.
	if ctx.Err() != nil {
		return false
	}
	a.evicting = true
	return true
}

func (a *app) endEvict() {
	a.idleLock.Lock()
	defer a.idleLock.Unlock()
	a.evicting = false
	if !a.closed.Load() {
		if a.endEvictHook != nil {
			a.endEvictHook()
		}
		a.touchIdle()
	}
}

// touchIdle bumps the actor's idle deadline and re-enqueues it in the idler
// queue so the deactivation timer restarts.
func (a *app) touchIdle() {
	a.idleAt.Store(new(a.clock.Now().Add(a.idleTimeout)))
	a.idlerQueue.Enqueue(a)
}
