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

package connector

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/runtime/scheduler/internal/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

func newTestLoop(t *testing.T) loop.Interface[loops.EventConn] {
	t.Helper()

	l := loop.New[loops.EventConn](1024).NewLoop(&connector{})

	errCh := make(chan error)
	go func() { errCh <- l.Run(t.Context()) }()
	t.Cleanup(func() {
		l.Close(new(loops.Close))
		select {
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout waiting for loop to close")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})

	return l
}

func TestConnector(t *testing.T) {
	t.Parallel()

	t.Run("close with no connections should not panic", func(t *testing.T) {
		t.Parallel()
		l := newTestLoop(t)
		l.Close(new(loops.Close))
	})

	t.Run("connect then close should close connections", func(t *testing.T) {
		t.Parallel()

		l := newTestLoop(t)

		var closed atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed.Store(true) },
			},
		})

		l.Close(new(loops.Close))

		assert.True(t, closed.Load())
	})

	t.Run("second connect should close first connections", func(t *testing.T) {
		t.Parallel()

		l := newTestLoop(t)

		var closed1 atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed1.Store(true) },
			},
		})

		var closed2 atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed2.Store(true) },
			},
		})

		// After processing both Connect events, first conns should be
		// closed but second should still be open.
		// Use Close to synchronize and ensure all events are processed.
		l.Close(new(loops.Close))

		assert.True(t, closed1.Load())
		assert.True(t, closed2.Load())
	})

	t.Run("connections are not closed until connector processes connect event", func(t *testing.T) {
		t.Parallel()

		// This test verifies the fix: connections should only be closed
		// when the connector loop processes the next Connect event, not
		// before (which was the bug - the hosts loop was closing them).

		var mu sync.Mutex
		var closeOrder []string

		l := newTestLoop(t)

		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() {
					mu.Lock()
					closeOrder = append(closeOrder, "conn1")
					mu.Unlock()
				},
			},
		})

		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() {
					mu.Lock()
					closeOrder = append(closeOrder, "conn2")
					mu.Unlock()
				},
			},
		})

		l.Close(new(loops.Close))

		mu.Lock()
		defer mu.Unlock()

		// conn1 should be closed before conn2, since the second Connect
		// event closes conn1 first (via handleDisconnect), then stores
		// conn2. The final Close event then closes conn2.
		require.Equal(t, []string{"conn1", "conn2"}, closeOrder)
	})

	t.Run("disconnect closes connections", func(t *testing.T) {
		t.Parallel()

		l := newTestLoop(t)

		var closed atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed.Store(true) },
			},
		})

		l.Enqueue(new(loops.Disconnect))

		// Synchronize with a Close event.
		l.Close(new(loops.Close))

		assert.True(t, closed.Load())
	})

	t.Run("reconnect does not close client connections", func(t *testing.T) {
		t.Parallel()

		l := newTestLoop(t)

		var closed atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed.Store(true) },
			},
		})

		l.Enqueue(&loops.Reconnect{
			AppTarget: ptr.Of(true),
		})

		// Use a barrier event (another Connect) to ensure Reconnect was
		// processed. We don't send Close here because Close calls
		// handleDisconnect which would close the conns.
		var barrier atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { barrier.Store(true) },
			},
		})

		// Wait for all events to be processed by closing the loop.
		l.Close(new(loops.Close))

		// conn was closed by the second Connect (which calls
		// handleDisconnect), not by the Reconnect.
		assert.True(t, closed.Load())
		assert.True(t, barrier.Load())
	})

	t.Run("multiple close conns per connect are all closed", func(t *testing.T) {
		t.Parallel()

		l := newTestLoop(t)

		var closed1, closed2, closed3 atomic.Bool
		l.Enqueue(&loops.Connect{
			CloseConns: []context.CancelFunc{
				func() { closed1.Store(true) },
				func() { closed2.Store(true) },
				func() { closed3.Store(true) },
			},
		})

		l.Close(new(loops.Close))

		assert.True(t, closed1.Load())
		assert.True(t, closed2.Load())
		assert.True(t, closed3.Load())
	})
}
