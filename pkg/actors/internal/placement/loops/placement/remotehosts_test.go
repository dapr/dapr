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

package placement

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	healthzfake "github.com/dapr/dapr/pkg/healthz/fake"
	"github.com/dapr/kit/events/loop"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

// watch hands r a watch of actorType and returns the channel r replied with.
func watch(t *testing.T, r *remoteHosts, actorType string) chan bool {
	t.Helper()
	return watchEvent(t, r, &loops.WatchRemoteHosts{ActorType: actorType, Ctx: t.Context()}, loopfake.New[loops.EventPlace]())
}

// watchEvent hands r the watch e, with placeLoop as the placement loop, and
// returns the channel r replied with on e.Resp.
func watchEvent(t *testing.T, r *remoteHosts, e *loops.WatchRemoteHosts, placeLoop loop.Interface[loops.EventPlace]) chan bool {
	t.Helper()
	resp := make(chan chan bool, 1)
	e.Resp = resp
	r.watch(e, placeLoop)
	select {
	case ch := <-resp:
		return ch
	default:
		require.Fail(t, "no reply")
		return nil
	}
}

func recvAnswer(t *testing.T, ch chan bool) bool {
	t.Helper()
	select {
	case v, ok := <-ch:
		require.True(t, ok, "channel closed")
		return v
	default:
		require.Fail(t, "no answer")
		return false
	}
}

func noAnswer(t *testing.T, ch chan bool) {
	t.Helper()
	select {
	case v, ok := <-ch:
		require.Failf(t, "unexpected answer", "%v (open %v)", v, ok)
	default:
	}
}

func roundCompleted(remote map[string]bool, pending ...string) *loops.RoundCompleted {
	e := &loops.RoundCompleted{Remote: remote, Pending: make(map[string]struct{})}
	for _, actorType := range pending {
		e.Pending[actorType] = struct{}{}
	}
	return e
}

func TestRemoteHosts(t *testing.T) {
	t.Run("a subscriber gets the answer of the next round, then only its changes", func(t *testing.T) {
		var r remoteHosts
		ch := watch(t, &r, "a")
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(nil))
		assert.False(t, recvAnswer(t, ch))

		r.roundCompleted(roundCompleted(map[string]bool{"b": true}))
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))
	})

	t.Run("a slow subscriber gets the latest answer only", func(t *testing.T) {
		var r remoteHosts
		ch := watch(t, &r, "a")
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		r.roundCompleted(roundCompleted(nil))
		assert.False(t, recvAnswer(t, ch))
		noAnswer(t, ch)
	})

	t.Run("a late subscriber gets the known answer at once", func(t *testing.T) {
		var r remoteHosts
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))

		a := watch(t, &r, "a")
		assert.True(t, recvAnswer(t, a))

		// No round showed b, so no other daprd hosts it.
		b := watch(t, &r, "b")
		assert.False(t, recvAnswer(t, b))
	})

	t.Run("a pending type keeps its last answer", func(t *testing.T) {
		var r remoteHosts
		ch := watch(t, &r, "a")
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))

		// The tables of a round still in progress no longer show a.
		r.roundCompleted(roundCompleted(nil, "a"))
		noAnswer(t, ch)

		late := watch(t, &r, "a")
		assert.True(t, recvAnswer(t, late))

		r.roundCompleted(roundCompleted(nil))
		assert.False(t, recvAnswer(t, ch))
		assert.False(t, recvAnswer(t, late))
	})

	t.Run("a type pending since the first round waits for its own round", func(t *testing.T) {
		var r remoteHosts
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}, "a"))

		ch := watch(t, &r, "a")
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(nil, "a"))
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))
	})

	t.Run("a round with every type pending changes nothing", func(t *testing.T) {
		var r remoteHosts
		ch := watch(t, &r, "a")
		r.roundCompleted(&loops.RoundCompleted{Remote: map[string]bool{"a": true}, PendingAll: true})
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))
		r.roundCompleted(&loops.RoundCompleted{PendingAll: true})
		noAnswer(t, ch)
	})

	t.Run("the watch ctx being done unwatches, which closes the channel", func(t *testing.T) {
		var r remoteHosts
		unwatches := make(chan *loops.UnwatchRemoteHosts, 1)
		placeLoop := loopfake.New[loops.EventPlace]().WithEnqueue(func(e loops.EventPlace) {
			unwatches <- e.(*loops.UnwatchRemoteHosts)
		})
		ctx, cancel := context.WithCancel(t.Context())
		ch := watchEvent(t, &r, &loops.WatchRemoteHosts{ActorType: "a", Ctx: ctx}, placeLoop)
		noAnswer(t, ch)

		cancel()
		var u *loops.UnwatchRemoteHosts
		select {
		case u = <-unwatches:
		case <-time.After(5 * time.Second):
			require.Fail(t, "no unwatch enqueued")
		}
		assert.Equal(t, "a", u.ActorType)
		assert.Equal(t, ch, u.Ch)

		r.unwatch(u.ActorType, u.Ch)
		_, ok := <-ch
		assert.False(t, ok)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.Empty(t, r.watchers)
	})
}

func TestRemoteHostsLoop(t *testing.T) {
	// handleWatch hands p a watch of actorType and returns the channel p
	// replied with.
	handleWatch := func(t *testing.T, p *placement, actorType string) chan bool {
		t.Helper()
		resp := make(chan chan bool, 1)
		require.NoError(t, p.Handle(t.Context(), &loops.WatchRemoteHosts{ActorType: actorType, Ctx: t.Context(), Resp: resp}))
		select {
		case ch := <-resp:
			return ch
		default:
			require.Fail(t, "no reply")
			return nil
		}
	}

	t.Run("the last answer survives stream loss", func(t *testing.T) {
		p := &placement{loop: loopfake.New[loops.EventPlace]()}
		require.NoError(t, p.Handle(t.Context(), roundCompleted(map[string]bool{"a": true})))

		// No stream: a new subscriber still gets the last answer.
		ch := handleWatch(t, p, "a")
		assert.True(t, recvAnswer(t, ch))
	})

	t.Run("shutdown closes the channels, and a later unwatch is a no-op", func(t *testing.T) {
		p := &placement{loop: loopfake.New[loops.EventPlace]()}
		a := handleWatch(t, p, "a")
		b := handleWatch(t, p, "b")
		require.NoError(t, p.Handle(t.Context(), &loops.Shutdown{}))
		for _, ch := range []chan bool{a, b} {
			_, ok := <-ch
			assert.False(t, ok)
		}

		require.NoError(t, p.Handle(t.Context(), &loops.UnwatchRemoteHosts{ActorType: "a", Ch: a}))
	})

	t.Run("the loop stopping on an error closes the channels", func(t *testing.T) {
		ready := &atomic.Bool{}
		p := &placement{
			ready:      ready,
			htarget:    healthzfake.New(),
			loop:       loopfake.New[loops.EventPlace](),
			dissLoop:   loopfake.New[loops.EventDiss](),
			actorTable: tablefake.New(),
			inflight:   inflight.New(inflight.Options{Hostname: "127.0.0.1", Port: "3500"}),
			idx:        1,
		}
		ch := handleWatch(t, p, "a")

		// The loop context is done, so the stream close returns its error and
		// the loop stops without handling Shutdown.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := p.Handle(ctx, &loops.ConnCloseStream{Error: errors.New("connection lost"), IDx: 1})
		require.ErrorIs(t, err, context.Canceled)

		_, ok := <-ch
		assert.False(t, ok)
	})
}
