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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/messages"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func Test_requestid(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("foobar", config.ReentrancyConfig{
		Enabled: true,
	})
	l := New(Options{
		ConfigStore: store,
		ActorType:   "foobar",
	})

	req := internalv1pb.NewInternalInvokeRequest("foo")

	errCh := make(chan error)
	go func() {
		_, cancel, err := l.LockRequest(t.Context(), req)
		errCh <- err
		cancel()
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 1, l.inflights.Front().depth)
		}
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := l.LockRequest(ctx, req)
	require.Error(t, err)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		assert.Fail(t, "lock not acquired")
	}
}

func Test_requestidcustom(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("foobar", config.ReentrancyConfig{
		Enabled:       true,
		MaxStackDepth: new(10),
	})
	l := New(Options{
		ConfigStore: store,
		ActorType:   "foobar",
	})

	req := internalv1pb.NewInternalInvokeRequest("foo")

	errCh := make(chan error)
	for range 10 {
		go func() {
			_, cancel, err := l.LockRequest(t.Context(), req)
			errCh <- err
			cancel()
		}()
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	_, _, err := l.LockRequest(t.Context(), req)
	require.Error(t, err)

	for range 10 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			assert.Fail(t, "lock not acquired")
		}
	}
}

func Test_ringid(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("foobar", config.ReentrancyConfig{
		Enabled:       true,
		MaxStackDepth: new(10),
	})
	l := New(Options{
		ConfigStore: store,
		ActorType:   "foobar",
	})

	req1 := internalv1pb.NewInternalInvokeRequest("foo")
	req2 := internalv1pb.NewInternalInvokeRequest("bar")

	errCh := make(chan error)
	for range 10 {
		go func() {
			_, cancel, err := l.LockRequest(t.Context(), req1)
			errCh <- err
			cancel()
		}()
		go func() {
			_, cancel, err := l.LockRequest(t.Context(), req2)
			errCh <- err
			cancel()
		}()
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		if assert.Equal(c, 2, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	for range 10 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			assert.Fail(t, "lock not acquired")
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	for range 10 {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second):
			assert.Fail(t, "lock not acquired")
		}
	}
}

func Test_header(t *testing.T) {
	t.Parallel()

	t.Run("with header", func(t *testing.T) {
		store := reentrancystore.New()
		store.Store("foobar", config.ReentrancyConfig{
			Enabled:       true,
			MaxStackDepth: new(10),
		})
		l := New(Options{
			ConfigStore: store,
			ActorType:   "foobar",
		})

		req := internalv1pb.NewInternalInvokeRequest("foo")
		_, cancel, err := l.LockRequest(t.Context(), req)
		require.NoError(t, err)
		t.Cleanup(cancel)
		assert.NotEmpty(t, req.GetMetadata()["Dapr-Reentrancy-Id"])
	})

	t.Run("without header", func(t *testing.T) {
		store := reentrancystore.New()
		store.Store("foobar", config.ReentrancyConfig{
			Enabled:       false,
			MaxStackDepth: new(10),
		})
		l := New(Options{
			ConfigStore: store,
			ActorType:   "foobar",
		})

		req := internalv1pb.NewInternalInvokeRequest("foo")
		_, cancel, err := l.LockRequest(t.Context(), req)
		require.NoError(t, err)
		t.Cleanup(cancel)
		assert.Empty(t, req.GetMetadata()["Dapr-Reentrancy-Id"])
	})
}

func Test_MaxStackDepthRejectedCallReleasesDepth(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	one := 1
	store.Store("foo", config.ReentrancyConfig{Enabled: true, MaxStackDepth: &one})
	l := New(Options{ActorType: "foo", ConfigStore: store})

	req := internalv1pb.NewInternalInvokeRequest("foo")
	_, release, err := l.LockRequest(t.Context(), req)
	require.NoError(t, err)
	_, _, err = l.LockRequest(t.Context(), req)
	require.ErrorIs(t, err, messages.ErrActorMaxStackDepthExceeded)
	release()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, rel, err := l.LockRequest(ctx, internalv1pb.NewInternalInvokeRequest("foo"))
	require.NoError(t, err, "actor lock stuck after a rejected reentrant call")
	rel()
}

func Test_CancelQueuedDoesNotReleaseRunning(t *testing.T) {
	t.Parallel()

	for _, reentrancy := range []bool{false, true} {
		t.Run(fmt.Sprintf("reentrancy=%t", reentrancy), func(t *testing.T) {
			t.Parallel()

			store := reentrancystore.New()
			store.Store("foobar", config.ReentrancyConfig{Enabled: reentrancy})
			l := New(Options{ConfigStore: store, ActorType: "foobar"})

			_, release1, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("foo"))
			require.NoError(t, err)

			acquired := make(chan error, 1)
			go func() {
				_, release, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("foo"))
				acquired <- err
				if err == nil {
					release()
				}
			}()
			waitLen(t, l, 2)

			errCh := make(chan error, 3)
			cancels := make([]context.CancelFunc, 3)
			for i := range cancels {
				ctx, cancel := context.WithCancel(t.Context())
				cancels[i] = cancel
				go func() {
					_, _, err := l.LockRequest(ctx, internalv1pb.NewInternalInvokeRequest("foo"))
					errCh <- err
				}()
			}
			waitLen(t, l, 5)

			for _, cancel := range cancels {
				cancel()
			}
			for range cancels {
				select {
				case err := <-errCh:
					require.ErrorIs(t, err, context.Canceled)
				case <-time.After(time.Second):
					require.Fail(t, "cancelled request did not return")
				}
			}

			select {
			case <-acquired:
				require.Fail(t, "queued request acquired the lock while the running turn still holds it")
			case <-time.After(100 * time.Millisecond):
			}

			release1()
			select {
			case err := <-acquired:
				require.NoError(t, err)
			case <-time.After(time.Second):
				require.Fail(t, "queued request did not acquire the lock after release")
			}
			waitLen(t, l, 0)
		})
	}
}

func Test_CancelQueuedReentrantRetrySameID(t *testing.T) {
	t.Parallel()

	store := reentrancystore.New()
	store.Store("foobar", config.ReentrancyConfig{Enabled: true})
	l := New(Options{ConfigStore: store, ActorType: "foobar"})

	_, release1, err := l.LockRequest(t.Context(), internalv1pb.NewInternalInvokeRequest("foo"))
	require.NoError(t, err)

	req := internalv1pb.NewInternalInvokeRequest("foo")
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, _, err := l.LockRequest(ctx, req)
		errCh <- err
	}()
	waitLen(t, l, 2)
	cancel()
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		require.Fail(t, "cancelled request did not return")
	}

	acquired := make(chan error, 1)
	go func() {
		_, release, err := l.LockRequest(t.Context(), req)
		acquired <- err
		if err == nil {
			release()
		}
	}()

	select {
	case <-acquired:
		require.Fail(t, "retried request acquired the lock while the running turn still holds it")
	case <-time.After(100 * time.Millisecond):
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		var depths []int
		l.inflights.Range(func(v *inflight) bool {
			depths = append(depths, v.depth)
			return true
		})
		<-l.lock
		assert.Equal(c, []int{1, 1}, depths)
	}, time.Second*5, time.Millisecond*10)

	release1()
	select {
	case err := <-acquired:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "retried request did not acquire the lock after release")
	}
	waitLen(t, l, 0)
}

func waitLen(t *testing.T, l *Lock, n int) {
	t.Helper()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		assert.Equal(c, n, l.inflights.Len())
		<-l.lock
	}, time.Second*5, time.Millisecond*10)
}
