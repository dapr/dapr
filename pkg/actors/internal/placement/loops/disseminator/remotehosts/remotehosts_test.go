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

package remotehosts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recv(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case v, ok := <-ch:
		require.True(t, ok, "channel closed")
		return v
	case <-time.After(5 * time.Second):
		require.Fail(t, "no update")
		return false
	}
}

func none(t *testing.T, ch <-chan bool) {
	t.Helper()
	select {
	case v := <-ch:
		require.Failf(t, "unexpected update", "%v", v)
	default:
	}
}

func TestWatch(t *testing.T) {
	t.Run("a subscriber gets the answer of the next round, then only its changes", func(t *testing.T) {
		w := New()
		ch := w.Subscribe(t.Context(), "a")
		none(t, ch)

		hosted := map[string]bool{"a": false}
		w.Update(func(at string) (bool, bool) { return hosted[at], true })
		assert.False(t, recv(t, ch))

		w.Update(func(at string) (bool, bool) { return hosted[at], true })
		none(t, ch)

		hosted["a"] = true
		w.Update(func(at string) (bool, bool) { return hosted[at], true })
		assert.True(t, recv(t, ch))
	})

	t.Run("a slow subscriber gets the latest answer only", func(t *testing.T) {
		w := New()
		ch := w.Subscribe(t.Context(), "a")
		w.Update(func(string) (bool, bool) { return true, true })
		w.Update(func(string) (bool, bool) { return false, true })
		assert.False(t, recv(t, ch))
		none(t, ch)
	})

	t.Run("a type without a settled answer keeps its last answer", func(t *testing.T) {
		w := New()
		ch := w.Subscribe(t.Context(), "a")
		w.Update(func(string) (bool, bool) { return true, false })
		none(t, ch)

		w.Update(func(string) (bool, bool) { return false, true })
		assert.False(t, recv(t, ch))
		w.Update(func(string) (bool, bool) { return true, false })
		none(t, ch)
	})

	t.Run("a late subscriber gets the known answer at once", func(t *testing.T) {
		w := New()
		first := w.Subscribe(t.Context(), "a")
		w.Update(func(string) (bool, bool) { return true, true })
		assert.True(t, recv(t, first))

		late := w.Subscribe(t.Context(), "a")
		assert.True(t, recv(t, late))

		other := w.Subscribe(t.Context(), "b")
		none(t, other)
	})

	t.Run("the channel closes with ctx and is no longer updated", func(t *testing.T) {
		w := New()
		ctx, cancel := context.WithCancel(t.Context())
		ch := w.Subscribe(ctx, "a")
		cancel()
		require.Eventually(t, func() bool {
			select {
			case _, ok := <-ch:
				return !ok
			default:
				return false
			}
		}, 5*time.Second, time.Millisecond)

		var asked bool
		w.Update(func(string) (bool, bool) { asked = true; return true, true })
		assert.False(t, asked)
	})
}
