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

package compstore

import (
	"sync"
	"testing"

	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/stretchr/testify/assert"
)

func TestNextSubscriberIndex(t *testing.T) {
	t.Run("sequential calls return incrementing values", func(t *testing.T) {
		store := New()

		id1 := store.NextSubscriberIndex()
		id2 := store.NextSubscriberIndex()
		id3 := store.NextSubscriberIndex()

		assert.Equal(t, rtpubsub.ConnectionID(1), id1)
		assert.Equal(t, rtpubsub.ConnectionID(2), id2)
		assert.Equal(t, rtpubsub.ConnectionID(3), id3)
	})

	t.Run("concurrent calls return unique values", func(t *testing.T) {
		store := New()
		const numGoroutines = 100
		const numCallsPerGoroutine = 10
		var wg sync.WaitGroup
		var mu sync.Mutex
		ids := make(map[rtpubsub.ConnectionID]bool)

		wg.Add(numGoroutines)
		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < numCallsPerGoroutine; j++ {
					id := store.NextSubscriberIndex()
					mu.Lock()
					ids[id] = true
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		assert.Len(t, ids, numGoroutines*numCallsPerGoroutine, "Expected all IDs to be unique")

		for i := 1; i <= numGoroutines*numCallsPerGoroutine; i++ {
			assert.True(t, ids[rtpubsub.ConnectionID(i)], "Expected ID %d to be present", i)
		}
	})
}
