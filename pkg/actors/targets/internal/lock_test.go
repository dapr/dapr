/*
Copyright 2024 The Dapr Authors
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

package internal

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

func Test_Lock(t *testing.T) {
	t.Parallel()

	l := NewLock(LockOptions{})
	cancel, err := l.Lock()
	require.NoError(t, err)
	cancel()

	cancel, err = l.Lock()
	require.NoError(t, err)
	cancel()

	l.Close()

	l = NewLock(LockOptions{})
	cancel1, err := l.Lock()
	require.NoError(t, err)

	errCh := make(chan error)
	var cancel2 context.CancelFunc
	go func() {
		cancel2, err = l.Lock()
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

	l := NewLock(LockOptions{ReentrancyEnabled: true})
	t.Cleanup(l.Close)

	req := invokev1.NewInvokeMethodRequest("foo")

	errCh := make(chan error)
	go func() {
		cancel, err := l.LockRequest(req)
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

	_, err := l.LockRequest(req)
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

	l := NewLock(LockOptions{
		ReentrancyEnabled: true,
		MaxStackDepth:     10,
	})
	t.Cleanup(l.Close)

	req := invokev1.NewInvokeMethodRequest("foo")

	errCh := make(chan error)
	for range 10 {
		go func() {
			cancel, err := l.LockRequest(req)
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

	_, err := l.LockRequest(req)
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

	l := NewLock(LockOptions{
		ReentrancyEnabled: true,
		MaxStackDepth:     10,
	})
	t.Cleanup(l.Close)

	req1 := invokev1.NewInvokeMethodRequest("foo")
	req2 := invokev1.NewInvokeMethodRequest("bar")

	errCh := make(chan error)
	for range 10 {
		go func() {
			cancel, err := l.LockRequest(req1)
			errCh <- err
			cancel()
		}()
		go func() {
			cancel, err := l.LockRequest(req2)
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
