// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

import (
	"sync"

	"github.com/pkg/errors"
	"go.uber.org/atomic"
)

var ErrMaxStackDepthExceeded error = errors.New("Maximum stack depth exceeded")

type ActorLock struct {
	methodLock    *sync.Mutex
	requestLock   *sync.Mutex
	activeRequest *string
	stackDepth    *atomic.Int32
	maxStackDepth int32
}

func NewActorLock(maxStackDepth int32) ActorLock {
	return ActorLock{
		methodLock:    &sync.Mutex{},
		requestLock:   &sync.Mutex{},
		activeRequest: nil,
		stackDepth:    atomic.NewInt32(int32(0)),
		maxStackDepth: maxStackDepth,
	}
}

func (a *ActorLock) Lock(requestID *string) error {
	currentRequest := a.getCurrentID()

	if a.stackDepth.Load() == a.maxStackDepth {
		return ErrMaxStackDepthExceeded
	}

	if currentRequest == nil || *currentRequest != *requestID {
		a.methodLock.Lock()
		a.setCurrentID(requestID)
		a.stackDepth.Inc()
	} else {
		a.stackDepth.Inc()
	}

	return nil
}

func (a *ActorLock) Unlock() {
	a.stackDepth.Dec()
	if a.stackDepth.Load() == 0 {
		a.clearCurrentID()
		a.methodLock.Unlock()
	}
}

func (a *ActorLock) getCurrentID() *string {
	a.requestLock.Lock()
	defer a.requestLock.Unlock()

	return a.activeRequest
}

func (a *ActorLock) setCurrentID(id *string) {
	a.requestLock.Lock()
	defer a.requestLock.Unlock()

	a.activeRequest = id
}

func (a *ActorLock) clearCurrentID() {
	a.requestLock.Lock()
	defer a.requestLock.Unlock()

	a.activeRequest = nil
}
