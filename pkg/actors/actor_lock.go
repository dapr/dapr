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
