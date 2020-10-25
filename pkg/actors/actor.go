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
	// releaseCh is the channel to signal when all pending actor calls are completed. This channel
	// is used when runtime drains actor.
	releaseCh chan struct{}

	once sync.Once
}

func newActor(actorType, actorID string) *actor {
	return &actor{
		actorType:         actorType,
		actorID:           actorID,
		concurrencyLock:   &sync.Mutex{},
		releaseCh:         nil,
		lastUsedTime:      time.Now().UTC(),
		pendingActorCalls: 0,
	}
}

// isBusy returns true when pending actor calls are ongoing.
func (a *actor) isBusy() bool {
	return a.pendingActorCalls > 0
}

// channel creates or get new release channel. this channel is used for draining the actor.
func (a *actor) channel() chan struct{} {
	a.once.Do(func() {
		a.releaseCh = make(chan struct{})
	})
	return a.releaseCh
}

// lock holds the lock for turn-based concurrency.
func (a *actor) lock() {
	atomic.AddInt32(&a.pendingActorCalls, int32(1))
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, a.pendingActorCalls)
	a.concurrencyLock.Lock()
	a.lastUsedTime = time.Now().UTC()
}

// unlock release the lock for turn-based concurrency. If releaseChannel is avaiable,
// it will close the channel to notify runtime to delete actor.
func (a *actor) unlock() {
	a.concurrencyLock.Unlock()
	if atomic.AddInt32(&a.pendingActorCalls, int32(-1)) == 0 && a.releaseCh != nil {
		close(a.releaseCh)
	}
	diag.DefaultMonitoring.ReportActorPendingCalls(a.actorType, a.pendingActorCalls)
}
