// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"sync"
	"sync/atomic"
	"time"

	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/pkg/errors"
)

var (
	// ErrActorDisposed is the error when runtime tries to hold the lock of the disposed actor.
	ErrActorDisposed error = errors.New("actor is already disposed")
)

// actor represents single actor object and maintains its turn-based concurrency.
type actor struct {
	// actorType is the type of actor.
	actorType string
	// actorID is the ID of actorType.
	actorID string

	// concurrencyLock is the lock to maintain actor's turn-based concurrency.
	concurrencyLock *sync.Mutex
	// pendingActorCalls is the number of the current pending actor calls by turn-based concurrency.
	pendingActorCalls int32

	// When consistent hashing tables are updated, actor runtime drains actor to rebalance actors
	// across actor hosts after drainOngoingCallTimeout or until all pending actor calls are completed.
	// lastUsedTime is the time when the last actor call holds lock. This is used to calculate
	// the duration of ongoing calls to time out.
	lastUsedTime time.Time

	// disposed is true when actor is already disposed.
	disposed bool
	// disposeCh is the channel to signal when all pending actor calls are completed. This channel
	// is used when runtime drains actor.
	disposeCh chan struct{}

	once sync.Once
}

func newActor(actorType, actorID string) *actor {
	return &actor{
		actorType:         actorType,
		actorID:           actorID,
		concurrencyLock:   &sync.Mutex{},
		disposeCh:         nil,
		disposed:          false,
		lastUsedTime:      time.Now().UTC(),
		pendingActorCalls: 0,
	}
}

// isBusy returns true when pending actor calls are ongoing.
func (a *actor) isBusy() bool {
	return !a.disposed && a.pendingActorCalls > 0
}

// channel creates or get new release channel. this channel is used for draining the actor.
func (a *actor) channel() chan struct{} {
	a.once.Do(func() {
		a.disposeCh = make(chan struct{})
	})
	return a.disposeCh
}

// lock holds the lock for turn-based concurrency.
func (a *actor) lock() error {
	atomic.AddInt32(&a.pendingActorCalls, int32(1))
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, a.pendingActorCalls)
	a.concurrencyLock.Lock()
	if a.disposed {
		return ErrActorDisposed
	}
	a.lastUsedTime = time.Now().UTC()
	return nil
}

// unlock release the lock for turn-based concurrency. If disposeCh is available,
// it will close the channel to notify runtime to dispose actor.
func (a *actor) unlock() {
	if atomic.AddInt32(&a.pendingActorCalls, int32(-1)) == 0 {
		if !a.disposed && a.disposeCh != nil {
			a.disposed = true
			close(a.disposeCh)
		}
	}
	a.concurrencyLock.Unlock()
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, a.pendingActorCalls)
}
