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
	tablefake "github.com/dapr/dapr/pkg/actors/table/fake"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func TestWatchRemoteHostsAfterRun(t *testing.T) {
	// newPlacement returns a placement whose loop drops every event, as a
	// closed loop does, but hands the watches to onWatch.
	newPlacement := func(onWatch func(*loops.WatchRemoteHosts)) *placement {
		return &placement{
			table:   tablefake.New(),
			watches: make(map[*loops.RemoteHostsCh]struct{}),
			loop: loopfake.New[loops.EventPlace]().WithEnqueue(func(e loops.EventPlace) {
				if w, ok := e.(*loops.WatchRemoteHosts); ok && onWatch != nil {
					onWatch(w)
				}
			}),
		}
	}
	closed := func(t *testing.T, ch <-chan bool) {
		t.Helper()
		select {
		case _, ok := <-ch:
			assert.False(t, ok)
		case <-time.After(5 * time.Second):
			require.Fail(t, "channel not closed")
		}
	}

	t.Run("a watch the loop never answered is closed once Run returns", func(t *testing.T) {
		p := newPlacement(nil)
		ch := p.WatchRemoteHosts(t.Context(), "a")
		select {
		case <-ch:
			require.Fail(t, "channel closed before Run returned")
		default:
		}

		require.NoError(t, p.Run(t.Context()))
		closed(t, ch)
		assert.Empty(t, p.watches)
	})

	t.Run("a watch after Run returned gets a closed channel", func(t *testing.T) {
		p := newPlacement(nil)
		require.NoError(t, p.Run(t.Context()))
		closed(t, p.WatchRemoteHosts(t.Context(), "a"))
		assert.Empty(t, p.watches)
	})

	t.Run("a channel the loop closed is not closed again", func(t *testing.T) {
		p := newPlacement(func(w *loops.WatchRemoteHosts) { w.Ch.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		ch := p.WatchRemoteHosts(ctx, "a")
		closed(t, ch)
		cancel()
		assert.Empty(t, p.watches)
		require.NoError(t, p.Run(t.Context()))
	})
}
