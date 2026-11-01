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

package lock

import (
	"context"
	"sync"

	"github.com/dapr/dapr/pkg/actors/targets/errors"
)

type Stallable struct {
	*Lock
	mu        sync.Mutex
	stalledCh chan struct{}
	releaseCh chan struct{}
}

func NewStallable() *Stallable {
	return &Stallable{
		Lock:      New(),
		stalledCh: make(chan struct{}),
	}
}

func (l *Stallable) ContextLock(ctx context.Context) (context.CancelFunc, error) {
	l.mu.Lock()
	stalledCh := l.stalledCh
	l.mu.Unlock()

	select {
	case l.ch <- struct{}{}:
	case <-l.closeCh:
		return nil, errors.NewClosed("lock")
	case <-stalledCh:
		return nil, errors.NewStalled()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return func() { <-l.ch }, nil
}

func (l *Stallable) Init() {
	l.Lock.Init()
	l.mu.Lock()
	l.stalledCh = make(chan struct{})
	l.releaseCh = nil
	l.mu.Unlock()
}

// Stall marks the lock as stalled and returns a channel which is closed by
// ReleaseStall to wake the stall holder, plus a cancel func which resets the
// stalled state.
func (l *Stallable) Stall() (<-chan struct{}, context.CancelFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-l.stalledCh:
	default:
		close(l.stalledCh)
	}

	release := make(chan struct{})
	l.releaseCh = release

	return release, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.stalledCh = make(chan struct{})
		if l.releaseCh == release {
			l.releaseCh = nil
		}
	}
}

// ReleaseStall resets the stalled state and wakes the stall holder, if any,
// so the actor holding the stall can be deactivated.
func (l *Stallable) ReleaseStall() {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-l.stalledCh:
		l.stalledCh = make(chan struct{})
	default:
	}

	if l.releaseCh != nil {
		close(l.releaseCh)
		l.releaseCh = nil
	}
}
