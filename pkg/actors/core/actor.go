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

package core

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actor")

// ErrActorDisposed is the error when runtime tries to hold the lock of the disposed actor.
var ErrActorDisposed = errors.New("actor is already disposed")

// actor represents single actor object and maintains its turn-based concurrency.
type Actor struct {
	// ActorType is the type of actor.
	ActorType string
	// ActorID is the ID of actorType.
	ActorID string

	// ActorLock is the lock to maintain actor's turn-based concurrency with allowance for reentrancy if configured.
	ActorLock *ActorLock
	// pendingActorCalls is the number of the current pending actor calls by turn-based concurrency.
	pendingActorCalls atomic.Int32

	// When consistent hashing tables are updated, actor runtime drains actor to rebalance actors
	// across actor hosts after drainOngoingCallTimeout or until all pending actor calls are completed.
	// LastUsedTime is the time when the last actor call holds lock. This is used to calculate
	// the duration of ongoing calls to time out.
	LastUsedTime time.Time

	// disposeLock guards disposed and disposeCh.
	disposeLock sync.RWMutex
	// disposed is true when actor is already disposed.
	disposed bool
	// disposeCh is the channel to signal when all pending actor calls are completed. This channel
	// is used when runtime drains actor.
	disposeCh chan struct{}

	Clock clock.Clock
}

// IsBusy returns true when pending actor calls are ongoing.
func (a *Actor) IsBusy() bool {
	a.disposeLock.RLock()
	disposed := a.disposed
	a.disposeLock.RUnlock()
	return !disposed && a.pendingActorCalls.Load() > 0
}

// channel creates or get new dispose channel. This channel is used for draining the actor.
func (a *Actor) Channel() chan struct{} {
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

// Lock holds the Lock for turn-based concurrency.
func (a *Actor) Lock(reentrancyID *string) error {
	pending := a.pendingActorCalls.Add(1)
	diag.DefaultMonitoring.ReportActorPendingCalls(a.ActorType, pending)

	err := a.ActorLock.Lock(reentrancyID)
	if err != nil {
		return err
	}

	a.disposeLock.RLock()
	disposed := a.disposed
	a.disposeLock.RUnlock()
	if disposed {
		a.Unlock()
		return ErrActorDisposed
	}
	a.LastUsedTime = a.Clock.Now().UTC()
	return nil
}

// Unlock releases the lock for turn-based concurrency. If disposeCh is available,
// it will close the channel to notify runtime to dispose actor.
func (a *Actor) Unlock() {
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

	a.ActorLock.Unlock()
	diag.DefaultMonitoring.ReportActorPendingCalls(a.ActorType, pending)
}
