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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/kit/events/loop"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

type nopHandler struct{}

func (nopHandler) Handle(context.Context, loops.EventPlace) error { return nil }

func TestWatchRemoteHosts(t *testing.T) {
	closed := func(t *testing.T, ch <-chan bool) {
		t.Helper()
		select {
		case _, ok := <-ch:
			assert.False(t, ok)
		case <-time.After(5 * time.Second):
			require.Fail(t, "channel not closed")
		}
	}

	t.Run("a watch returns the channel the loop replies with", func(t *testing.T) {
		want := make(chan bool, 1)
		p := &placement{
			loop: loopfake.New[loops.EventPlace]().WithEnqueue(func(e loops.EventPlace) {
				w, ok := e.(*loops.WatchRemoteHosts)
				require.True(t, ok)
				assert.Equal(t, "a", w.ActorType)
				w.Resp <- want
			}),
		}
		assert.Equal(t, (<-chan bool)(want), p.WatchRemoteHosts(t.Context(), "a"))
	})

	t.Run("a watch after placement stopped returns a closed channel once ctx is done", func(t *testing.T) {
		// A closed loop drops what is enqueued on it, so the watch is never
		// handled.
		l := loop.New[loops.EventPlace](1).NewLoop(nopHandler{})
		errCh := make(chan error, 1)
		go func() { errCh <- l.Run(t.Context()) }()
		l.Close(&loops.Shutdown{})
		require.NoError(t, <-errCh)

		p := &placement{loop: l}
		ctx, cancel := context.WithCancel(t.Context())
		chCh := make(chan (<-chan bool), 1)
		go func() { chCh <- p.WatchRemoteHosts(ctx, "a") }()

		select {
		case <-chCh:
			require.Fail(t, "watch returned before ctx was done")
		case <-time.After(100 * time.Millisecond):
		}

		cancel()
		select {
		case ch := <-chCh:
			closed(t, ch)
		case <-time.After(5 * time.Second):
			require.Fail(t, "watch did not return once ctx was done")
		}
	})
}
