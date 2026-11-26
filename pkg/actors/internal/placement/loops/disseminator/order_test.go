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

package disseminator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/timeout"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedfake "github.com/dapr/dapr/pkg/runtime/scheduler/client/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// newTestDisseminator creates a disseminator with minimal dependencies for testing
// the handleOrder method.
func newTestDisseminator(t *testing.T) (*disseminator, *healthzfake.Fake, *schedfake.Fake) {
	t.Helper()

	ht := healthzfake.New()
	sched := schedfake.New()

	inf := inflight.New(inflight.Options{
		Hostname: "localhost",
		Port:     "3500",
	})

	streamLoop := loopfake.New[loops.EventStream]()

	dissLoop := LoopFactoryCache.NewLoop(nil)

	diss := &disseminator{
		namespace:        "default",
		id:               "test-id",
		actorTable:       tablefake.New(),
		scheduler:        sched,
		healthTarget:     ht,
		inflight:         inf,
		streamLoop:       streamLoop,
		currentOperation: loops.OrderLock,
		currentVersion:   0,
		timeout:          time.Second * 30,
		ready:            new(atomic.Bool),

		roundChangedTypes: make(map[string]struct{}),
		v2Rounds:          make(map[uint64]*v2Round),
		v2Completed:       make(map[uint64]struct{}),
		placementLoop:     loopfake.New[loops.EventPlace](),
		halting:           make(map[string]int),
		pendingHalts:      make(map[*loops.HaltActorTypes]struct{}),
		closeCh:           make(chan struct{}),
	}

	diss.loop = dissLoop
	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    dissLoop,
		Timeout: time.Second * 30,
	})

	return diss, ht, sched
}

// haltTest is a disseminator whose loop events are received on events, with
// the claim-tracking loop running.
func haltTest(t *testing.T) (*disseminator, chan loops.EventDiss) {
	t.Helper()

	diss, _, _ := newTestDisseminator(t)
	events := make(chan loops.EventDiss, 10)
	diss.loop = loopfake.New[loops.EventDiss]().WithEnqueue(func(e loops.EventDiss) { events <- e })
	diss.inflight.Open(t.Context())
	t.Cleanup(func() {
		diss.inflight.Close(nil)
		diss.wg.Wait()
	})
	return diss, events
}

func lockRequest(t *testing.T, actorType string) (*loops.LockRequest, chan *loops.LockResponse) {
	t.Helper()
	ch := make(chan *loops.LockResponse, 1)
	return &loops.LockRequest{ActorType: actorType, Context: t.Context(), Response: ch}, ch
}

func recvDrained(t *testing.T, events chan loops.EventDiss) *loops.HaltActorTypesDrained {
	t.Helper()
	select {
	case e := <-events:
		drained, ok := e.(*loops.HaltActorTypesDrained)
		require.True(t, ok, "unexpected event %T", e)
		return drained
	case <-time.After(time.Second * 5):
		require.Fail(t, "the drain did not complete")
		return nil
	}
}

func TestHandleHaltActorTypes(t *testing.T) {
	t.Run("halts once drained, and keeps the types of a round blocked", func(t *testing.T) {
		diss, events := haltTest(t)

		// A type a dissemination round blocks stays blocked after the halt.
		diss.roundChangedTypes["inround"] = struct{}{}
		diss.inflight.LockTypes([]string{"inround"})

		var blockedDuringHalt []bool
		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound", "inround"},
			Halt: func(context.Context) error {
				blockedDuringHalt = []bool{diss.inflight.IsBlocked("bound"), diss.inflight.IsBlocked("inround")}
				return errors.New("halt error")
			},
			Done: done,
		})
		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))

		require.EqualError(t, <-done, "halt error")
		// New calls of the halted types wait while their actors halt.
		assert.Equal(t, []bool{true, true}, blockedDuringHalt)
		assert.False(t, diss.inflight.IsBlocked("bound"))
		assert.True(t, diss.inflight.IsBlocked("inround"))
		assert.Empty(t, diss.halting)
		assert.Empty(t, diss.pendingHalts)
	})

	t.Run("the drain does not hold the loop", func(t *testing.T) {
		diss, events := haltTest(t)

		// An in-flight call of the bound type.
		inflightCall, inflightCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(inflightCall)
		claim := <-inflightCallResp

		var halted atomic.Bool
		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { halted.Store(true); return nil },
			Done:       done,
		})
		assert.True(t, diss.inflight.IsBlocked("bound"))

		// Another actor type is served during the drain.
		other, otherResp := lockRequest(t, "other")
		diss.handleAcquireRequest(other)
		select {
		case resp := <-otherResp:
			resp.Cancel(nil)
		case <-time.After(time.Second * 5):
			require.Fail(t, "the drain held up another actor type")
		}

		// So is a state call of the bound type, which lets the in-flight
		// call save its state and complete.
		stateCall, stateCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(stateCall)
		select {
		case resp := <-stateCallResp:
			resp.Cancel(nil)
		case <-time.After(time.Second * 5):
			require.Fail(t, "the drain held up a state call of the bound type")
		}

		// A new call of the bound type waits for the halt.
		lookupResp := make(chan *loops.LookupResponse, 1)
		lookup := &loops.LookupRequest{
			Request:  &api.LookupActorRequest{ActorType: "bound", ActorID: "1"},
			Context:  t.Context(),
			Response: lookupResp,
		}
		diss.handleLookupRequest(lookup)

		select {
		case e := <-events:
			require.Fail(t, "unexpected event before the call completed", "%T", e)
		case <-lookupResp:
			require.Fail(t, "a new call of the bound type was not held")
		case <-time.After(time.Millisecond * 50):
		}
		assert.False(t, halted.Load())

		claim.Cancel(nil)
		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.True(t, halted.Load())
		assert.False(t, diss.inflight.IsBlocked("bound"))
		select {
		case <-lookupResp:
		case <-time.After(time.Second * 5):
			require.Fail(t, "the held call was not released after the halt")
		}
	})

	t.Run("a round completing during the halt leaves the type blocked", func(t *testing.T) {
		diss, events := haltTest(t)

		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return nil },
			Done:       done,
		})
		drained := recvDrained(t, events)

		diss.unlockRoundTypes([]string{"bound", "moved"})
		assert.True(t, diss.inflight.IsBlocked("bound"))

		diss.handleHaltActorTypesDrained(t.Context(), drained)
		require.NoError(t, <-done)
		assert.False(t, diss.inflight.IsBlocked("bound"))
	})

	// A round whose scope holds the type, while its table is unchanged, does
	// not drain the call in flight. Once it completes, the state call it
	// queued is released, or the call in flight could not complete.
	t.Run("a round during the halt queues the state calls of the type until it completes", func(t *testing.T) {
		diss, events := haltTest(t)
		diss.schedulerPlacement = true

		inflightCall, inflightCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(inflightCall)
		claim := <-inflightCallResp

		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return nil },
			Done:       done,
		})

		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderLock, Version: 1, Scope: []string{"bound"}, Partial: true},
		}))

		stateCall, stateCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(stateCall)
		select {
		case <-stateCallResp:
			require.Fail(t, "a state call of a type in a round was not queued")
		case <-time.After(time.Millisecond * 50):
		}

		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderUpdate, Version: 1, Partial: true},
		}))
		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderUnlock, Version: 1, Partial: true},
		}))

		// The halt still blocks the type, but no longer its state calls.
		assert.True(t, diss.inflight.IsBlocked("bound"))
		select {
		case resp := <-stateCallResp:
			resp.Cancel(nil)
		case <-time.After(time.Second * 5):
			require.Fail(t, "the queued state call was not released when the round completed")
		}

		claim.Cancel(nil)
		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.False(t, diss.inflight.IsBlocked("bound"))
	})

	t.Run("a halt completing during a round leaves the type blocked", func(t *testing.T) {
		diss, events := haltTest(t)
		diss.schedulerPlacement = true

		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return nil },
			Done:       done,
		})
		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderLock, Version: 1, Scope: []string{"bound"}, Partial: true},
		}))

		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.True(t, diss.inflight.IsBlocked("bound"))

		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderUpdate, Version: 1, Partial: true},
		}))
		require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{Op: loops.OrderUnlock, Version: 1, Partial: true},
		}))
		assert.False(t, diss.inflight.IsBlocked("bound"))
	})

	// A type which only a halt blocks is not waiting for a round, so it does
	// not hold back readiness.
	t.Run("a halt does not hold back readiness", func(t *testing.T) {
		diss, events := haltTest(t)
		diss.schedulerPlacement = true
		diss.actorTable = tablefake.New().WithTypes(func() []string { return []string{"bound"} })

		inflightCall, inflightCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(inflightCall)
		claim := <-inflightCallResp

		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return nil },
			Done:       done,
		})

		for _, op := range []loops.OrderOp{loops.OrderLock, loops.OrderUpdate, loops.OrderUnlock} {
			require.NoError(t, diss.handleOrderV2(t.Context(), &loops.StreamOrder{
				Order: &loops.Order{Op: op, Version: 1, Scope: []string{"other"}, Partial: true},
			}))
		}
		assert.True(t, diss.ready.Load())

		claim.Cancel(nil)
		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
	})

	// Before the first round of a stream there are no claims to drain, and
	// calls queue.
	t.Run("without a claim-tracking loop the halt runs at once", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)
		events := make(chan loops.EventDiss, 10)
		diss.loop = loopfake.New[loops.EventDiss]().WithEnqueue(func(e loops.EventDiss) { events <- e })
		t.Cleanup(diss.wg.Wait)

		done := make(chan error, 1)
		diss.handleHaltActorTypes(&loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return nil },
			Done:       done,
		})

		stateCall, stateCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(stateCall)
		select {
		case <-stateCallResp:
			require.Fail(t, "a state call was served without a claim-tracking loop")
		case <-time.After(time.Millisecond * 50):
		}

		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.False(t, diss.inflight.IsBlocked("bound"))

		diss.inflight.Open(t.Context())
		t.Cleanup(func() { diss.inflight.Close(nil) })
		select {
		case resp := <-stateCallResp:
			resp.Cancel(nil)
		case <-time.After(time.Second * 5):
			require.Fail(t, "the queued state call was not released")
		}
	})

	t.Run("overlapping halts unblock the type when the last one completes", func(t *testing.T) {
		diss, events := haltTest(t)

		done := make(chan error, 2)
		for range 2 {
			diss.handleHaltActorTypes(&loops.HaltActorTypes{
				ActorTypes: []string{"bound"},
				Halt:       func(context.Context) error { return nil },
				Done:       done,
			})
		}

		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.True(t, diss.inflight.IsBlocked("bound"))

		diss.handleHaltActorTypesDrained(t.Context(), recvDrained(t, events))
		require.NoError(t, <-done)
		assert.False(t, diss.inflight.IsBlocked("bound"))
	})

	t.Run("shutdown runs the halts still draining", func(t *testing.T) {
		diss, events := haltTest(t)

		inflightCall, inflightCallResp := lockRequest(t, "bound")
		diss.handleAcquireRequest(inflightCall)
		claim := <-inflightCallResp
		t.Cleanup(func() { claim.Cancel(nil) })

		done := make(chan error, 1)
		e := &loops.HaltActorTypes{
			ActorTypes: []string{"bound"},
			Halt:       func(context.Context) error { return errors.New("halted on shutdown") },
			Done:       done,
		}
		diss.handleHaltActorTypes(e)

		diss.completePendingHalts(t.Context())
		require.EqualError(t, <-done, "halted on shutdown")
		assert.False(t, diss.inflight.IsBlocked("bound"))

		// The drain which ends later does not halt again.
		diss.wg.Wait()
		assert.Empty(t, events)
		diss.handleHaltActorTypesDrained(t.Context(), &loops.HaltActorTypesDrained{Halt: e})
		assert.Empty(t, done)
	})
}

func TestHandleTimeout(t *testing.T) {
	t.Run("matching version closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.timeoutVersion = 3
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})
		assert.True(t, streamClosed, "stream should be closed on matching timeout version")
	})

	t.Run("old version is ignored", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.timeoutVersion = 5
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{Version: 3})
		assert.False(t, streamClosed, "stream should not be closed for stale timeout version")
	})
}

func TestHandleOrder_UpdateVersionMismatch(t *testing.T) {
	t.Run("update with lower version closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		diss.currentVersion = 10
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderUpdate,
				Version: 5,
			},
		})
		require.NoError(t, err)
		assert.True(t, streamClosed, "stream should be closed on version mismatch")
	})
}

func TestHandleOrder_UnknownOperation(t *testing.T) {
	t.Run("unknown operation closes stream", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op: loops.OrderOp(99),
			},
		})
		require.NoError(t, err)
		assert.True(t, streamClosed, "stream should be closed on unknown operation")
	})
}

func TestHandleTimeout_UpdateDequeuesTimeout(t *testing.T) {
	t.Run("UPDATE arriving before timeout dequeues the timeout", func(t *testing.T) {
		diss, _, _ := newTestDisseminator(t)

		var streamClosed bool
		diss.streamLoop = loopfake.New[loops.EventStream]().
			WithClose(func(loops.EventStream) {
				streamClosed = true
			})

		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderLock,
				Version: 5,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:       loops.OrderUpdate,
				Version:  5,
				V1Tables: &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		savedVersion := diss.timeoutVersion - 1
		diss.handleTimeout(t.Context(), &loops.DisseminationTimeout{
			Version: savedVersion,
		})
		assert.False(t, streamClosed, "timeout should be ignored after UPDATE dequeued it")
	})
}

func TestHandleOrder_UnlockVersionMismatch(t *testing.T) {
	t.Run("unlock with lower version is ignored and currentVersion is preserved", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// Simulate a LOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderLock,
				Version: 10,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, uint64(10), diss.currentVersion)

		// Simulate an UPDATE at version 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:       loops.OrderUpdate,
				Version:  10,
				V1Tables: &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, loops.OrderUpdate, diss.currentOperation)

		// Attempt UNLOCK with version 5 (lower than currentVersion=10).
		// This should be ignored and currentVersion should remain 10.
		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderUnlock,
				Version: 5,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should not be updated when unlock version is lower")
		assert.Equal(t, loops.OrderUpdate, diss.currentOperation,
			"operation should remain UPDATE when unlock is ignored")
		assert.False(t, ht.ReadyCalled(),
			"health target should not be marked ready when unlock is ignored")
	})

	t.Run("unlock with matching version succeeds and updates currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE -> UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderLock,
				Version: 10,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:       loops.OrderUpdate,
				Version:  10,
				V1Tables: &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderUnlock,
				Version: 10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should be set to matching unlock version")
		assert.Equal(t, loops.OrderUnlock, diss.currentOperation)
		assert.True(t, ht.ReadyCalled(),
			"health target should be marked ready on successful unlock")
	})

	t.Run("unlock with higher version succeeds and advances currentVersion", func(t *testing.T) {
		diss, ht, _ := newTestDisseminator(t)

		// LOCK -> UPDATE at version 5, UNLOCK at version 10.
		err := diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderLock,
				Version: 5,
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:       loops.OrderUpdate,
				Version:  5,
				V1Tables: &v1pb.PlacementTables{},
			},
		})
		require.NoError(t, err)

		err = diss.handleOrder(t.Context(), &loops.StreamOrder{
			Order: &loops.Order{
				Op:      loops.OrderUnlock,
				Version: 10,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, uint64(10), diss.currentVersion,
			"currentVersion should advance to higher unlock version")
		assert.Equal(t, loops.OrderUnlock, diss.currentOperation)
		assert.True(t, ht.ReadyCalled())
	})
}
