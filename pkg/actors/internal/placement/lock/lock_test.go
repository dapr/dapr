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

package lock

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Lock(t *testing.T) {
	t.Parallel()

	t.Run("can rlock multiple times", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go l.Run(ctx)

		ctx1, c1, err := l.RLock(ctx)
		require.NoError(t, err)
		ctx2, c2, err := l.RLock(ctx)
		require.NoError(t, err)
		ctx3, c3, err := l.RLock(ctx)
		require.NoError(t, err)

		require.NoError(t, ctx1.Err())
		require.NoError(t, ctx2.Err())
		require.NoError(t, ctx3.Err())

		c1()
		require.Error(t, ctx1.Err())
		require.NoError(t, ctx2.Err())
		require.NoError(t, ctx3.Err())

		c2()
		require.Error(t, ctx1.Err())
		require.Error(t, ctx2.Err())
		require.NoError(t, ctx3.Err())

		c3()
		require.Error(t, ctx1.Err())
		require.Error(t, ctx2.Err())
		require.Error(t, ctx3.Err())
	})

	t.Run("rlock unlock removes cancel state", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go l.Run(ctx)

		_, c1, err := l.RLock(ctx)
		require.NoError(t, err)
		_, c2, err := l.RLock(ctx)
		require.NoError(t, err)
		_, c3, err := l.RLock(ctx)
		require.NoError(t, err)

		assert.Len(t, l.rcancels, 3)
		c1()
		assert.Len(t, l.rcancels, 2)
		c2()
		assert.Len(t, l.rcancels, 1)
		c3()
		assert.Empty(t, l.rcancels, 0)
	})

	t.Run("calling lock cancels all current rlocks", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go l.Run(ctx)

		ctx1, _, err := l.RLock(ctx)
		require.NoError(t, err)
		ctx2, _, err := l.RLock(ctx)
		require.NoError(t, err)
		ctx3, _, err := l.RLock(ctx)
		require.NoError(t, err)

		require.NoError(t, ctx1.Err())
		require.NoError(t, ctx2.Err())
		require.NoError(t, ctx3.Err())

		mcancel := l.Lock()
		require.Error(t, ctx1.Err())
		require.Error(t, ctx2.Err())
		require.Error(t, ctx3.Err())
		mcancel()

		assert.Empty(t, l.rcancels)
	})

	t.Run("rlock when closed should error", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		go l.Run(ctx)

		select {
		case <-l.closeCh:
		case <-time.After(time.Second * 5):
			assert.Fail(t, "expected close")
		}

		_, _, err := l.RLock(context.Background())
		require.Error(t, err)
	})

	t.Run("lock continues to work after close", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		l.Run(ctx)

		lcancel := l.Lock()
		lcancel()
		lcancel = l.Lock()
		lcancel()
	})

	t.Run("rlock blocks until outter unlocks", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go l.Run(ctx)

		lcancel := l.Lock()

		gotRLock := make(chan struct{})

		errCh := make(chan error, 1)
		go func() {
			_, c1, err := l.RLock(ctx)
			errCh <- err
			t.Cleanup(c1)
			close(gotRLock)
		}()
		t.Cleanup(func() {
			require.NoError(t, <-errCh)
		})

		select {
		case <-time.After(time.Millisecond * 500):
		case <-gotRLock:
			require.Fail(t, "unexpected rlock")
		}

		lcancel()
	})

	t.Run("lock blocks until outter unlocks", func(t *testing.T) {
		t.Parallel()

		l := New()

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)

		go l.Run(ctx)

		lcancel := l.Lock()

		gotLock := make(chan struct{})

		go func() {
			lockcancel := l.Lock()
			t.Cleanup(lockcancel)
			close(gotLock)
		}()

		select {
		case <-time.After(time.Millisecond * 500):
		case <-gotLock:
			require.Fail(t, "unexpected rlock")
		}

		lcancel()
	})
}
