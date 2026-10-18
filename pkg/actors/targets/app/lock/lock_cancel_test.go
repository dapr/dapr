//go:build unit

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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/config"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

// Test_queuedCancel ensures that a request which is cancelled while it is
// queued behind a running turn neither starts another queued request while
// the running turn still holds the lock, nor is started itself, and that the
// queue keeps its order once the running turn releases the lock.
func Test_queuedCancel(t *testing.T) {
	t.Parallel()

	for name, reentrancy := range map[string]bool{
		"reentrancy disabled": false,
		"reentrancy enabled":  true,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := reentrancystore.New()
			store.Store("foobar", config.ReentrancyConfig{
				Enabled:       reentrancy,
				MaxStackDepth: new(10),
			})
			l := New(Options{
				ConfigStore: store,
				ActorType:   "foobar",
			})

			// The running turn.
			_, releaseA, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("a"))
			require.NoError(t, err)

			// Queue B, C and D behind the running turn, in that order.
			acquiredB := queueRequest(t, l, t.Context(), "b", 2)
			ctxC, cancelC := context.WithCancel(t.Context())
			defer cancelC()
			acquiredC := queueRequest(t, l, ctxC, "c", 3)
			acquiredD := queueRequest(t, l, t.Context(), "d", 4)

			// C is cancelled by its caller while it is still queued.
			cancelC()
			select {
			case res := <-acquiredC:
				require.ErrorIs(t, res.err, context.Canceled)
			case <-time.After(time.Second * 5):
				require.Fail(t, "cancelled request did not return")
			}

			requireNotAcquired(t, acquiredB, "B started while A still holds the lock")
			requireNotAcquired(t, acquiredD, "D started while A still holds the lock")

			releaseA()
			releaseB := requireAcquired(t, acquiredB, "B did not start after A released the lock")
			requireNotAcquired(t, acquiredD, "D started while B still holds the lock")

			releaseB()
			releaseD := requireAcquired(t, acquiredD, "D did not start after B released the lock")
			releaseD()

			ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
			defer cancel()
			_, releaseE, err := l.LockRequest(ctx, internalv1pb.NewInternalInvokeRequest("e"))
			require.NoError(t, err, "actor lock stuck after a cancelled queued request")
			releaseE()

			l.lock <- struct{}{}
			assert.Equal(t, 0, l.inflights.Len())
			<-l.lock
		})
	}
}

type lockResult struct {
	release context.CancelFunc
	err     error
}

// queueRequest calls LockRequest in the background and waits until the request
// is queued, which is when the lock holds queueLen inflight requests.
func queueRequest(t *testing.T, l *Lock, ctx context.Context, method string, queueLen int) <-chan lockResult {
	t.Helper()

	ch := make(chan lockResult, 1)
	go func() {
		_, release, err := l.LockRequest(ctx, internalv1pb.NewInternalInvokeRequest(method))
		ch <- lockResult{release: release, err: err}
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		assert.Equal(c, queueLen, l.inflights.Len())
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	return ch
}

func requireAcquired(t *testing.T, ch <-chan lockResult, msg string) context.CancelFunc {
	t.Helper()

	select {
	case res := <-ch:
		require.NoError(t, res.err)
		return res.release
	case <-time.After(time.Second * 5):
		require.Fail(t, msg)
		return nil
	}
}

func requireNotAcquired(t *testing.T, ch <-chan lockResult, msg string) {
	t.Helper()

	select {
	case res := <-ch:
		if res.release != nil {
			res.release()
		}
		require.Fail(t, msg)
	case <-time.After(time.Millisecond * 200):
	}
}
