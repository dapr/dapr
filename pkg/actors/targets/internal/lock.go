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

package internal

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var (
	log = logger.NewLogger("dapr.runtime.actors.lock")

	// ErrActorDisposed is the error when runtime tries to hold the lock of the disposed actor.
	ErrActorDisposed = errors.New("actor is already disposed")

	ErrMaxStackDepthExceeded = errors.New("maximum stack depth exceeded")
)

type Lock struct {
	actorType string

	requestLock sync.Mutex
	reentrancy  config.ReentrancyConfig

	activeRequest *string
	stackDepth    atomic.Int32
	maxStackDepth int32
	// lockChan is used instead of a sync.Mutex to enforce FIFO ordering of method execution.
	// We use a buffered channel to ensure that requests are processed
	// in the order they arrive, which sync.Mutex does not guarantee.
	lockChan chan struct{}

	// disposeLock guards disposed and disposeCh.
	disposeLock sync.RWMutex

	// disposed is true when actor is already disposed.
	disposed bool
	// disposeCh is the channel to signal when all pending actor calls are completed. This channel
	// is used when runtime drains actor.
	disposeCh chan struct{}

	// pendingActorCalls is the number of the current pending actor calls by turn-based concurrency.
	pendingActorCalls atomic.Int32
}

func NewLock(maxStackDepth int32) *Lock {
	return &Lock{
		lockChan:      make(chan struct{}, 1),
		maxStackDepth: maxStackDepth,
	}
}

func (l *Lock) Lock(req *invokev1.InvokeMethodRequest) error {
	pending := l.pendingActorCalls.Add(1)
	diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, pending)

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if l.reentrancy.Enabled {
		if md := req.Metadata()["Dapr-Reentrancy-Id"]; md != nil && len(md.GetValues()) > 0 {
			reentrancyID = ptr.Of(md.GetValues()[0])
		} else {
			var uuidObj uuid.UUID
			var err error
			uuidObj, err = uuid.NewRandom()
			if err != nil {
				return fmt.Errorf("failed to generate UUID: %w", err)
			}
			uuidStr := uuidObj.String()
			req.AddMetadata(map[string][]string{
				"Dapr-Reentrancy-Id": {uuidStr},
			})
			reentrancyID = &uuidStr
		}
	}

	currentRequest := l.getCurrentID()

	if l.stackDepth.Load() == l.maxStackDepth {
		return ErrMaxStackDepthExceeded
	}

	if currentRequest == nil || *currentRequest != *reentrancyID {
		l.lockChan <- struct{}{}
		l.setCurrentID(reentrancyID)
		l.stackDepth.Add(1)
	} else {
		l.stackDepth.Add(1)
	}

	l.disposeLock.RLock()
	disposed := l.disposed
	l.disposeLock.RUnlock()
	if disposed {
		l.Unlock()
		return ErrActorDisposed
	}

	return nil
}

func (l *Lock) Unlock() {
	pending := l.pendingActorCalls.Add(-1)
	if pending == 0 {
		l.disposeLock.Lock()
		if !l.disposed && l.disposeCh != nil {
			l.disposed = true
			close(l.disposeCh)
		}
		l.disposeLock.Unlock()
	} else if pending < 0 {
		log.Error("BUGBUG: tried to unlock actor before locking actor.")
		return
	}

	l.stackDepth.Add(-1)
	if l.stackDepth.Load() == 0 {
		l.clearCurrentID()
		<-l.lockChan
	}

	diag.DefaultMonitoring.ReportActorPendingCalls(l.actorType, pending)
}

// Channel creates or get new dispose channel. This channel is used for draining the actor.
func (l *Lock) Channel() <-chan struct{} {
	l.disposeLock.RLock()
	disposeCh := l.disposeCh
	l.disposeLock.RUnlock()

	if disposeCh == nil {
		// If disposeCh is nil, acquire a write lock and retry
		// We need to retry after acquiring a write lock because another goroutine could race us
		l.disposeLock.Lock()
		disposeCh = l.disposeCh
		if disposeCh == nil {
			disposeCh = make(chan struct{})
			l.disposeCh = disposeCh
		}
		l.disposeLock.Unlock()
	}

	return disposeCh
}

func (l *Lock) getCurrentID() *string {
	l.requestLock.Lock()
	defer l.requestLock.Unlock()

	return l.activeRequest
}

func (l *Lock) setCurrentID(id *string) {
	l.requestLock.Lock()
	defer l.requestLock.Unlock()

	l.activeRequest = id
}

func (l *Lock) clearCurrentID() {
	l.requestLock.Lock()
	defer l.requestLock.Unlock()

	l.activeRequest = nil
}

// IsBusy returns true when pending actor calls are ongoing.
func (l *Lock) IsBusy() bool {
	l.disposeLock.RLock()
	disposed := l.disposed
	l.disposeLock.RUnlock()
	return !disposed && l.pendingActorCalls.Load() > 0
}
