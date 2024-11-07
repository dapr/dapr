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
)

func Test_Lock(t *testing.T) {
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
		assert.Equal(c, 1, l.pending)
		l.lock.Unlock()
	}, time.Second*5, time.Millisecond*10)

	time.Sleep(time.Second)

	cancel1()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		assert.Fail(t, "lock not acquired")
	}
	cancel2()
}

func Test_LockThree(t *testing.T) {
	l := NewLock(LockOptions{})
	cancel1, err := l.Lock()
	require.NoError(t, err)

	errCh := make(chan error)
	var cancel2, cancel3 context.CancelFunc
	go func() {
		var gerr error
		cancel2, gerr = l.Lock()
		errCh <- gerr
	}()
	go func() {
		var gerr error
		cancel3, gerr = l.Lock()
		errCh <- gerr
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		l.lock.Lock()
		assert.Equal(c, 2, l.pending)
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

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second):
		assert.Fail(t, "lock not acquired")
	}

	cancel3()

	l.Close()
}
