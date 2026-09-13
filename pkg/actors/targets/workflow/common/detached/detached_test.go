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

package detached

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Runner(t *testing.T) {
	t.Parallel()

	t.Run("runs on the root context and waits", func(t *testing.T) {
		t.Parallel()
		r := New(t.Context())

		var ran atomic.Int32
		for range 3 {
			require.True(t, r.Go(func(ctx context.Context) {
				ran.Add(1)
				assert.NoError(t, ctx.Err())
			}))
		}
		r.Wait()
		assert.Equal(t, int32(3), ran.Load())
	})

	t.Run("does not start once the root context is done", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r := New(ctx)

		assert.False(t, r.Go(func(context.Context) {
			require.Fail(t, "must not run after shutdown")
		}))
		r.Wait()
	})

	t.Run("close cancels in-flight work and refuses new work", func(t *testing.T) {
		t.Parallel()
		r := New(t.Context())

		entered := make(chan struct{})
		var cancelled atomic.Bool
		require.True(t, r.Go(func(ctx context.Context) {
			close(entered)
			<-ctx.Done()
			cancelled.Store(true)
		}))
		<-entered
		r.Close()
		assert.True(t, cancelled.Load())
		assert.False(t, r.Go(func(context.Context) {}))
	})

	t.Run("keyed work is deduplicated while in flight", func(t *testing.T) {
		t.Parallel()
		r := New(t.Context())

		release := make(chan struct{})
		entered := make(chan struct{})
		var runs atomic.Int32
		started, inflight := r.GoKeyed("k", func(context.Context) {
			runs.Add(1)
			close(entered)
			<-release
		})
		require.True(t, started)
		require.False(t, inflight)
		<-entered

		started, inflight = r.GoKeyed("k", func(context.Context) { runs.Add(1) })
		assert.False(t, started)
		assert.True(t, inflight)
		assert.True(t, r.InFlight("k"))

		close(release)
		r.Wait()
		assert.Equal(t, int32(1), runs.Load())
		assert.False(t, r.InFlight("k"))

		started, inflight = r.GoKeyed("k", func(context.Context) { runs.Add(1) })
		assert.True(t, started)
		assert.False(t, inflight)
		r.Wait()
		assert.Equal(t, int32(2), runs.Load())
	})
}
