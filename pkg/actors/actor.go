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

type actor struct {
	actorType string
	actorID   string

	concurrencyLock *sync.RWMutex
	lastUsedTime    time.Time
	busy            bool
	busyCh          chan (bool)

	pendingLockCount int32
}

func newActor(actorType, actorID string) *actor {
	return &actor{
		actorType:       actorType,
		actorID:         actorID,
		concurrencyLock: &sync.RWMutex{},
		busy:            false,
		busyCh:          make(chan bool, 1),
		lastUsedTime:    time.Now().UTC(),
	}
}

func (a *actor) isBusy() bool {
	return a.busy
}

func (a *actor) channel() chan (bool) {
	return a.busyCh
}

func (a *actor) lock() {
	atomic.AddInt32(&a.pendingLockCount, 1)
	diag.DefaultMonitoring.ReportCurrentPendingLocks(a.actorType, a.actorID, a.pendingLockCount)
	a.concurrencyLock.Lock()

	a.busy = true
	a.busyCh = make(chan bool, 1)
	a.lastUsedTime = time.Now().UTC()
}

func (a *actor) unLock() {
	if a.busy {
		a.busy = false
		close(a.busyCh)
	}

	a.concurrencyLock.Unlock()
	atomic.AddInt32(&a.pendingLockCount, -1)
	diag.DefaultMonitoring.ReportCurrentPendingLocks(a.actorType, a.actorID, a.pendingLockCount)
}
