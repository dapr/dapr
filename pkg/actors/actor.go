/*
Copyright 2021 The Dapr Authors
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

package actors

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/ptr"
)

// ErrActorDisposed is the error when runtime tries to hold the lock of the disposed actor.
var ErrActorDisposed = errors.New("actor is already disposed")

// actor represents single actor object and maintains its turn-based concurrency.
type actor struct {
	// actorType is the type of actor.
	actorType string
	// actorID is the ID of actorType.
	actorID string

	// actorLock is the lock to maintain actor's turn-based concurrency with allowance for reentrancy if configured.
	actorLock *ActorLock
	// pendingActorCalls is the number of the current pending actor calls by turn-based concurrency.
	pendingActorCalls atomic.Int32

	// idleTimeout is the configured max idle time for actors of this kind.
	idleTimeout time.Duration

	// idleAt is the time after which this actor is considered to be idle.
	// When the actor is locked, idleAt is updated by adding the idleTimeout to the current time.
	idleAt atomic.Pointer[time.Time]

	// disposeLock guards disposed and disposeCh.
	disposeLock sync.RWMutex
	// disposed is true when actor is already disposed.
	disposed bool
	// disposeCh is the channel to signal when all pending actor calls are completed. This channel
	// is used when runtime drains actor.
	disposeCh chan struct{}

	clock clock.Clock
}

func newActor(actorType, actorID string, maxReentrancyDepth *int, idleTimeout time.Duration, cl clock.Clock) *actor {
	if cl == nil {
		cl = &clock.RealClock{}
	}

	a := &actor{
		actorType:   actorType,
		actorID:     actorID,
		actorLock:   NewActorLock(int32(*maxReentrancyDepth)),
		clock:       cl,
		idleTimeout: idleTimeout,
	}
	a.idleAt.Store(ptr.Of(cl.Now().Add(idleTimeout)))

	return a
}

// Key returns the key for this unique actor.
// This is implemented to comply with the queueable interface.
func (a *actor) Key() string {
	return a.actorType + daprSeparator + a.actorID
}

// ScheduledTime returns the time the actor becomes idle at.
// This is implemented to comply with the queueable interface.
func (a *actor) ScheduledTime() time.Time {
	return *a.idleAt.Load()
}

// isBusy returns true when pending actor calls are ongoing.
func (a *actor) isBusy() bool {
	a.disposeLock.RLock()
	disposed := a.disposed
	a.disposeLock.RUnlock()
	return !disposed && a.pendingActorCalls.Load() > 0
}

// channel creates or get new dispose channel. This channel is used for draining the actor.
func (a *actor) channel() chan struct{} {
	a.disposeLock.RLock()
	disposeCh := a.disposeCh
	a.disposeLock.RUnlock()

	if disposeCh == nil {
		// If disposeCh is nil, acquire a write lock and retry
		// We need to retry after acquiring a write lock because another goroutine could race us
		a.disposeLock.Lock()
		disposeCh = a.disposeCh
		if disposeCh == nil {
			disposeCh = make(chan struct{})
			a.disposeCh = disposeCh
		}
		a.disposeLock.Unlock()
	}

	return disposeCh
}

// lock holds the lock for turn-based concurrency.
func (a *actor) lock(reentrancyID *string) error {
	pending := a.pendingActorCalls.Add(1)
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, pending)

	err := a.actorLock.Lock(reentrancyID)
	if err != nil {
		return err
	}

	a.disposeLock.RLock()
	disposed := a.disposed
	a.disposeLock.RUnlock()
	if disposed {
		a.unlock()
		return ErrActorDisposed
	}

	a.idleAt.Store(ptr.Of(a.clock.Now().Add(a.idleTimeout)))
	return nil
}

// unlock releases the lock for turn-based concurrency. If disposeCh is available,
// it will close the channel to notify runtime to dispose actor.
func (a *actor) unlock() {
	pending := a.pendingActorCalls.Add(-1)
	if pending == 0 {
		a.disposeLock.Lock()
		if !a.disposed && a.disposeCh != nil {
			a.disposed = true
			close(a.disposeCh)
		}
		a.disposeLock.Unlock()
	} else if pending < 0 {
		log.Error("BUGBUG: tried to unlock actor before locking actor.")
		return
	}

	a.actorLock.Unlock()
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, pending)
}
