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

package store

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/hashing/rendezvous"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func fakeLoop() loop.Interface[loops.EventStream] {
	return loopfake.New[loops.EventStream]()
}

func TestGetByKey(t *testing.T) {
	t.Parallel()

	t.Run("routes to the rendezvous owner host's streams", func(t *testing.T) {
		t.Parallel()
		i := newInstance()

		addrA, addrB := "10.0.0.1:50002", "10.0.0.2:50002"
		connA1, connA2, connB := fakeLoop(), fakeLoop(), fakeLoop()
		i.add("t1", connA1, &addrA)
		i.add("t1", connA2, &addrA)
		i.add("t1", connB, &addrB)

		table := rendezvous.New([]string{addrA, addrB})

		for k := range 100 {
			key := "actor-" + strconv.Itoa(k)
			owner, ok := table.Lookup(key)
			require.True(t, ok)

			got, ok := i.getByKey("t1", key)
			require.True(t, ok)
			if owner == addrA {
				assert.Contains(t, []loop.Interface[loops.EventStream]{connA1, connA2}, got)
			} else {
				assert.Equal(t, connB, got)
			}
		}
	})

	t.Run("addressless stream forces round robin over all streams", func(t *testing.T) {
		t.Parallel()
		i := newInstance()

		addrA := "10.0.0.1:50002"
		connA, connOld := fakeLoop(), fakeLoop()
		i.add("t1", connA, &addrA)
		cancel := i.add("t1", connOld, nil)

		seen := map[loop.Interface[loops.EventStream]]int{}
		for k := range 10 {
			got, ok := i.getByKey("t1", "actor-"+strconv.Itoa(k))
			require.True(t, ok)
			seen[got]++
		}
		assert.Len(t, seen, 2, "round robin must cover the addressless stream")

		// Once the old stream leaves, routing becomes owner aware: a single
		// host owns everything.
		cancel()
		for k := range 10 {
			got, ok := i.getByKey("t1", "actor-"+strconv.Itoa(k))
			require.True(t, ok)
			assert.Equal(t, connA, got)
		}
	})

	t.Run("unknown type returns false", func(t *testing.T) {
		t.Parallel()
		i := newInstance()
		_, ok := i.getByKey("t1", "a")
		assert.False(t, ok)
	})

	t.Run("host removal rebuilds the table", func(t *testing.T) {
		t.Parallel()
		i := newInstance()

		addrA, addrB := "10.0.0.1:50002", "10.0.0.2:50002"
		connA, connB := fakeLoop(), fakeLoop()
		cancelA := i.add("t1", connA, &addrA)
		i.add("t1", connB, &addrB)

		cancelA()
		for k := range 10 {
			got, ok := i.getByKey("t1", "actor-"+strconv.Itoa(k))
			require.True(t, ok)
			assert.Equal(t, connB, got)
		}
	})
}
