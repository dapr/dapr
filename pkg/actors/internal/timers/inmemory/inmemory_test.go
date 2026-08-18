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

package inmemory

import (
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
)

// TestUpdateActiveTimersCountConcurrent exercises updateActiveTimersCount from
// many goroutines using distinct actor types. First-seen actor types insert a
// new key into the activeTimersCount map, so this makes concurrent map writes
// overlap the map read used to increment the counter. Run with -race to catch a
// regression of the concurrent map read/write data race.
func TestUpdateActiveTimersCountConcurrent(t *testing.T) {
	i, ok := New(Options{Router: routerfake.New()}).(*inmemory)
	require.True(t, ok)

	const actorTypes = 200
	var wg sync.WaitGroup
	wg.Add(actorTypes)
	for a := range actorTypes {
		go func(a int) {
			defer wg.Done()
			i.updateActiveTimersCount("actor-"+strconv.Itoa(a), 1)
		}(a)
	}
	wg.Wait()

	for a := range actorTypes {
		assert.Equal(t, int64(1), i.GetActiveTimersCount("actor-"+strconv.Itoa(a)))
	}
}

// TestUpdateActiveTimersCountConcurrentSameType checks that concurrent updates
// for a single actor type are counted correctly, guarding the atomic counter
// against lost updates.
func TestUpdateActiveTimersCountConcurrentSameType(t *testing.T) {
	i, ok := New(Options{Router: routerfake.New()}).(*inmemory)
	require.True(t, ok)

	const increments = 500
	var wg sync.WaitGroup
	wg.Add(increments)
	for range increments {
		go func() {
			defer wg.Done()
			i.updateActiveTimersCount("actor-type", 1)
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(increments), i.GetActiveTimersCount("actor-type"))
}
