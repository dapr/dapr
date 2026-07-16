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
	ActorType string
	Context   context.Context
	Cancel    context.CancelCauseFunc
}

type Acquire struct {
	ActorType string
	Context   context.Context
	RespCh    chan *Claim
}

type releaseClaim struct {
	idx uint64
}

type CloseLock struct {
	Error                 error
	Timeout               *time.Duration
	DrainRebalancedActors *bool
}

// CancelTypes drains in-flight claims for the given set of actor types using
// the same grace-period semantics as CloseLock, but does not terminate the
// lock loop. Done is closed when the operation completes.
//
// PerTypeTimeouts overrides Timeout per actor type when present; types
// without an entry fall back to Timeout, then to a 2-second default. Each
// actor type drains in parallel against its own timer so the round wall-clock
// is bounded by the largest per-type drain, not the sum.
type CancelTypes struct {
	Types                 map[string]struct{}
	Error                 error
	Timeout               *time.Duration
	PerTypeTimeouts       map[string]time.Duration
	DrainRebalancedActors *bool
	Done                  chan struct{}
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
	case *CancelTypes:
		l.handleCancelTypes(e)
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
		ActorType: event.ActorType,
		Context:   ctx,
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

// handleCancelTypes drains all in-flight claims whose ActorType is in the
// requested set. Same grace-period semantics as handleClose but the lock
// loop continues running after this returns. Done is closed when the
// operation finishes so callers can wait synchronously.
//
// Claims are grouped by actor type and drained in parallel: each group runs
// against its own timer, using event.PerTypeTimeouts when set for that type
// and falling back to event.Timeout otherwise. Total wall-clock is bounded
// by the largest per-type drain, not the sum.
func (l *lock) handleCancelTypes(event *CancelTypes) {
	defer close(event.Done)

	if len(event.Types) == 0 {
		return
	}

	byType := make(map[string][]*Claim)
	for _, claim := range l.acquires {
		if _, ok := event.Types[claim.ActorType]; ok {
			byType[claim.ActorType] = append(byType[claim.ActorType], claim)
		}
	}
	if len(byType) == 0 {
		return
	}

	if event.DrainRebalancedActors != nil && !*event.DrainRebalancedActors {
		for _, claims := range byType {
			for _, claim := range claims {
				claim.Cancel(event.Error)
			}
		}
		return
	}

	defaultTimeout := time.Second * 2
	if event.Timeout != nil {
		defaultTimeout = *event.Timeout
	}

	var wg sync.WaitGroup
	for actorType, claims := range byType {
		timeout := defaultTimeout
		if t, ok := event.PerTypeTimeouts[actorType]; ok {
			timeout = t
		}

		wg.Go(func() {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			for _, claim := range claims {
				select {
				case <-claim.Context.Done():
				case <-timer.C:
					log.Errorf("Timed out waiting for actor type '%s' in-flight lock claims to be released for rebalanced types, force cancelling remaining claims", actorType)
					for _, claim := range claims {
						claim.Cancel(event.Error)
					}
					return
				}
			}
		})
	}
	wg.Wait()
}
