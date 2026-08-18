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

package actors

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUniqueEventTimestamp(t *testing.T) {
	t.Run("sequential calls are strictly increasing", func(t *testing.T) {
		abe := &Actors{}
		prev := abe.uniqueEventTimestamp().AsTime().UnixNano()
		for range 1000 {
			next := abe.uniqueEventTimestamp().AsTime().UnixNano()
			assert.Greater(t, next, prev)
			prev = next
		}
	})

	// Concurrent RaiseEvent ingestion must never hand out the same timestamp,
	// otherwise dedup.IsDuplicateExternalEvent would drop a distinct event that
	// happens to race onto the same wall-clock nanosecond.
	t.Run("concurrent calls are all unique", func(t *testing.T) {
		abe := &Actors{}
		const n = 500
		out := make([]int64, n)
		var wg sync.WaitGroup
		wg.Add(n)
		for i := range n {
			go func(i int) {
				defer wg.Done()
				out[i] = abe.uniqueEventTimestamp().AsTime().UnixNano()
			}(i)
		}
		wg.Wait()

		seen := make(map[int64]struct{}, n)
		for _, v := range out {
			_, dup := seen[v]
			require.False(t, dup, "duplicate timestamp issued: %d", v)
			seen[v] = struct{}{}
		}
	})
}
