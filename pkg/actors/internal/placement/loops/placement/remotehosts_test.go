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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
)

func recvAnswer(t *testing.T, ch <-chan bool) bool {
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

func noAnswer(t *testing.T, ch <-chan bool) {
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
		ch := make(chan bool, 1)
		r.watch("a", ch)
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
		ch := make(chan bool, 1)
		r.watch("a", ch)
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		r.roundCompleted(roundCompleted(nil))
		assert.False(t, recvAnswer(t, ch))
		noAnswer(t, ch)
	})

	t.Run("a late subscriber gets the known answer at once", func(t *testing.T) {
		var r remoteHosts
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))

		a := make(chan bool, 1)
		r.watch("a", a)
		assert.True(t, recvAnswer(t, a))

		// No round showed b, so no other daprd hosts it.
		b := make(chan bool, 1)
		r.watch("b", b)
		assert.False(t, recvAnswer(t, b))
	})

	t.Run("a pending type keeps its last answer", func(t *testing.T) {
		var r remoteHosts
		ch := make(chan bool, 1)
		r.watch("a", ch)
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))

		// The tables of a round still in progress no longer show a.
		r.roundCompleted(roundCompleted(nil, "a"))
		noAnswer(t, ch)

		late := make(chan bool, 1)
		r.watch("a", late)
		assert.True(t, recvAnswer(t, late))

		r.roundCompleted(roundCompleted(nil))
		assert.False(t, recvAnswer(t, ch))
		assert.False(t, recvAnswer(t, late))
	})

	t.Run("a type pending since the first round waits for its own round", func(t *testing.T) {
		var r remoteHosts
		r.roundCompleted(roundCompleted(map[string]bool{"a": true}, "a"))

		ch := make(chan bool, 1)
		r.watch("a", ch)
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(nil, "a"))
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))
	})

	t.Run("a round with every type pending changes nothing", func(t *testing.T) {
		var r remoteHosts
		ch := make(chan bool, 1)
		r.watch("a", ch)
		r.roundCompleted(&loops.RoundCompleted{Remote: map[string]bool{"a": true}, PendingAll: true})
		noAnswer(t, ch)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.True(t, recvAnswer(t, ch))
		r.roundCompleted(&loops.RoundCompleted{PendingAll: true})
		noAnswer(t, ch)
	})

	t.Run("unwatch closes the channel and it is no longer answered", func(t *testing.T) {
		var r remoteHosts
		ch := make(chan bool, 1)
		r.watch("a", ch)
		r.unwatch("a", ch)
		_, ok := <-ch
		assert.False(t, ok)

		r.roundCompleted(roundCompleted(map[string]bool{"a": true}))
		assert.Empty(t, r.watchers)
	})
}

func TestRemoteHostsLoop(t *testing.T) {
	t.Run("the last answer survives stream loss", func(t *testing.T) {
		p := &placement{}
		require.NoError(t, p.Handle(t.Context(), roundCompleted(map[string]bool{"a": true})))

		// No stream: a new subscriber still gets the last answer.
		ch := make(chan bool, 1)
		require.NoError(t, p.Handle(t.Context(), &loops.WatchRemoteHosts{ActorType: "a", Ch: ch}))
		assert.True(t, recvAnswer(t, ch))
	})

	t.Run("shutdown closes the channels, and a later unwatch is a no-op", func(t *testing.T) {
		p := &placement{}
		ch := make(chan bool, 1)
		require.NoError(t, p.Handle(t.Context(), &loops.WatchRemoteHosts{ActorType: "a", Ch: ch}))
		require.NoError(t, p.Handle(t.Context(), &loops.Shutdown{}))
		_, ok := <-ch
		assert.False(t, ok)

		require.NoError(t, p.Handle(t.Context(), &loops.UnwatchRemoteHosts{ActorType: "a", Ch: ch}))
	})
}
