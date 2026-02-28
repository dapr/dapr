/*
Copyright 2026 The Dapr Authors
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
	"fmt"
	"sync"
	"time"

	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("dapr.runtime.actors.loops.disseminator.inflight.lock")

	LoopFactory = loop.New[Event](1024)
	lockCache   = sync.Pool{
		New: func() any {
			return &lock{
				acquires: make(map[uint64]*Claim),
			}
		},
	}
)

type Event any

type Claim struct {
	Context context.Context
	Cancel  context.CancelCauseFunc
}

type Acquire struct {
	Context context.Context
	RespCh  chan *Claim
}

type releaseClaim struct {
	idx uint64
}

type CloseLock struct {
	Error                 error
	Timeout               *time.Duration
	DrainRebalancedActors *bool
}

type lock struct {
	idx      uint64
	acquires map[uint64]*Claim

	loop loop.Interface[Event]
}

func New() loop.Interface[Event] {
	l := lockCache.Get().(*lock)
	l.loop = LoopFactory.NewLoop(l)
	return l.loop
}

func (l *lock) Handle(_ context.Context, event Event) error {
	switch e := event.(type) {
	case *Acquire:
		l.handleAcquire(e)
	case *releaseClaim:
		l.handleRelease(e)
	case *CloseLock:
		l.handleClose(e)
	default:
		panic(fmt.Sprintf("unknown lock event type: %T", e))
	}

	return nil
}

func (l *lock) handleClose(closeLock *CloseLock) {
	defer func() {
		clear(l.acquires)
		lockCache.Put(l)
	}()

	// If drainRebalancedActors is false, immediately cancel all claims without
	// waiting.
	// Default to true (drain) if not specified.
	if closeLock.DrainRebalancedActors != nil && !*closeLock.DrainRebalancedActors {
		for _, claim := range l.acquires {
			claim.Cancel(closeLock.Error)
		}
		return
	}

	// Grace period to allow claims to be released.
	// Default to 2 seconds if no timeout is provided.
	timeout := time.Second * 2
	if closeLock.Timeout != nil {
		timeout = *closeLock.Timeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for _, claim := range l.acquires {
		select {
		case <-claim.Context.Done():
		case <-timer.C:
			log.Errorf("Timed out waiting for actor in-flight lock claims to be released, force cancelling remaining claims")
			// Force cancel all remaining claims after timeout.
			for _, claim := range l.acquires {
				claim.Cancel(closeLock.Error)
			}
			return
		}
	}
}

func (l *lock) handleRelease(release *releaseClaim) {
	delete(l.acquires, release.idx)
}

func (l *lock) handleAcquire(event *Acquire) {
	idx := l.idx
	l.idx++

	var done bool

	ctx, cancel := context.WithCancelCause(event.Context)
	claim := &Claim{
		Context: ctx,
		Cancel: func(err error) {
			if done {
				return
			}
			done = true
			cancel(err)
			l.loop.Enqueue(&releaseClaim{idx: idx})
		},
	}

	l.acquires[idx] = claim
	event.RespCh <- claim
}
