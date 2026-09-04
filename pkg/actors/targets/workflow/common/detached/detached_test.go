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
		ctx, cancel := context.WithCancel(t.Context())
		r := New(ctx)

		var ran atomic.Int32
		var sawRoot atomic.Bool
		for range 3 {
			require.True(t, r.Go(func(c context.Context) {
				ran.Add(1)
				sawRoot.Store(c == ctx)
			}))
		}
		r.Wait()
		assert.Equal(t, int32(3), ran.Load())
		assert.True(t, sawRoot.Load(), "fn must receive the root context")
		cancel()
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
}
