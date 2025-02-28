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

package locker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

func Test_Lock(t *testing.T) {
	t.Parallel()

	l := newLock(lockOptions{})
	cancel, err := l.baseLock()
	require.NoError(t, err)
	cancel()

	cancel, err = l.baseLock()
	require.NoError(t, err)
	cancel()

	l.close()

	l = newLock(lockOptions{})
	cancel1, err := l.baseLock()
	require.NoError(t, err)

	errCh := make(chan error)
	var cancel2 context.CancelFunc
	go func() {
		cancel2, err = l.baseLock()
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock.Lock()
		assert.Equal(c, 2, l.inflights.Len())
		l.lock.Unlock()
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

	l := newLock(lockOptions{reentrancyEnabled: true})
	t.Cleanup(l.close)

	req := internalv1pb.NewInternalInvokeRequest("foo")

	errCh := make(chan error)
	go func() {
		cancel, err := l.lockRequest(req)
		errCh <- err
		cancel()
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock.Lock()
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 1, l.inflights.Front().depth)
		}
		l.lock.Unlock()
	}, time.Second*5, time.Millisecond*10)

	_, err := l.lockRequest(req)
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

	l := newLock(lockOptions{
		reentrancyEnabled: true,
		maxStackDepth:     10,
	})
	t.Cleanup(l.close)

	req := internalv1pb.NewInternalInvokeRequest("foo")

	errCh := make(chan error)
	for range 10 {
		go func() {
			cancel, err := l.lockRequest(req)
			errCh <- err
			cancel()
		}()
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock.Lock()
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		l.lock.Unlock()
	}, time.Second*5, time.Millisecond*10)

	_, err := l.lockRequest(req)
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

	l := newLock(lockOptions{
		reentrancyEnabled: true,
		maxStackDepth:     10,
	})
	t.Cleanup(l.close)

	req1 := internalv1pb.NewInternalInvokeRequest("foo")
	req2 := internalv1pb.NewInternalInvokeRequest("bar")

	errCh := make(chan error)
	for range 10 {
		go func() {
			cancel, err := l.lockRequest(req1)
			errCh <- err
			cancel()
		}()
		go func() {
			cancel, err := l.lockRequest(req2)
			errCh <- err
			cancel()
		}()
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock.Lock()
		if assert.Equal(c, 2, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		l.lock.Unlock()
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
		l.lock.Lock()
		if assert.Equal(c, 1, l.inflights.Len()) {
			assert.Equal(c, 10, l.inflights.Front().depth)
		}
		l.lock.Unlock()
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
		l := newLock(lockOptions{
			reentrancyEnabled: true,
			maxStackDepth:     10,
		})
		t.Cleanup(l.close)

		req := internalv1pb.NewInternalInvokeRequest("foo")
		cancel, err := l.lockRequest(req)
		require.NoError(t, err)
		t.Cleanup(cancel)
		assert.NotEmpty(t, req.GetMetadata()["Dapr-Reentrancy-Id"])
	})

	t.Run("without header", func(t *testing.T) {
		l := newLock(lockOptions{
			reentrancyEnabled: false,
			maxStackDepth:     10,
		})
		t.Cleanup(l.close)

		req := internalv1pb.NewInternalInvokeRequest("foo")
		cancel, err := l.lockRequest(req)
		require.NoError(t, err)
		t.Cleanup(cancel)
		assert.Empty(t, req.GetMetadata()["Dapr-Reentrancy-Id"])
	})
}
