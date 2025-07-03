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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/internal/reentrancystore"
	"github.com/dapr/dapr/pkg/config"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/kit/ptr"
)

func Test_Lock(t *testing.T) {
	t.Parallel()

	l := New(Options{
		ConfigStore: reentrancystore.New(),
	})
	_, cancel, err := l.Lock(t.Context())
	require.NoError(t, err)
	cancel()

	_, cancel, err = l.Lock(t.Context())
	require.NoError(t, err)
	cancel()

	l = New(Options{
		ConfigStore: reentrancystore.New(),
	})
	_, cancel1, err := l.Lock(t.Context())
	require.NoError(t, err)

	errCh := make(chan error)
	var cancel2 context.CancelFunc
	go func() {
		_, cancel2, err = l.Lock(t.Context())
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock <- struct{}{}
		assert.Equal(c, 2, l.inflights.Len())
		<-l.lock
	}, time.Second*5, time.Millisecond*10)

	cancel1()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		assert.Fail(t, "lock not acquired")
	}
	cancel2()
}

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
		MaxStackDepth: ptr.Of(10),
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
		MaxStackDepth: ptr.Of(10),
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
			MaxStackDepth: ptr.Of(10),
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
			MaxStackDepth: ptr.Of(10),
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
