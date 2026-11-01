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

package rendezvous

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hostN(n int) string {
	return "10.0." + strconv.Itoa(n/256) + "." + strconv.Itoa(n%256) + ":50002"
}

func hosts(n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = hostN(i)
	}
	return out
}

func TestLookup(t *testing.T) {
	t.Parallel()

	t.Run("empty and nil tables have no owner", func(t *testing.T) {
		t.Parallel()
		_, ok := New(nil).Lookup("actor-1")
		assert.False(t, ok)
		var nilTable *Table
		_, ok = nilTable.Lookup("actor-1")
		assert.False(t, ok)
	})

	t.Run("single host owns every key", func(t *testing.T) {
		t.Parallel()
		table := New([]string{"10.0.0.1:50002"})
		for i := range 100 {
			owner, ok := table.Lookup("actor-" + strconv.Itoa(i))
			require.True(t, ok)
			assert.Equal(t, "10.0.0.1:50002", owner)
		}
	})

	t.Run("deterministic and independent of input order or duplicates", func(t *testing.T) {
		t.Parallel()
		a := New([]string{hostN(0), hostN(1), hostN(2)})
		b := New([]string{hostN(2), hostN(0), hostN(1), hostN(0)})
		require.True(t, a.Equal(b))
		for i := range 1000 {
			key := "actor-" + strconv.Itoa(i)
			ownerA, okA := a.Lookup(key)
			ownerB, okB := b.Lookup(key)
			require.True(t, okA)
			require.True(t, okB)
			assert.Equal(t, ownerA, ownerB)
			assert.Contains(t, a.Hosts(), ownerA)
		}
	})
}

func TestEqual(t *testing.T) {
	t.Parallel()

	assert.True(t, New(nil).Equal(New(nil)))
	assert.True(t, New([]string{"a", "b"}).Equal(New([]string{"b", "a", "a"})))
	assert.False(t, New([]string{"a"}).Equal(New([]string{"a", "b"})))
	assert.False(t, New([]string{"a"}).Equal(New([]string{"b"})))
}

func TestBalance(t *testing.T) {
	t.Parallel()

	const numHosts = 10
	const numKeys = 100_000

	table := New(hosts(numHosts))
	counts := make(map[string]int, numHosts)
	for i := range numKeys {
		owner, ok := table.Lookup("actor-" + strconv.Itoa(i))
		require.True(t, ok)
		counts[owner]++
	}

	require.Len(t, counts, numHosts)
	expected := numKeys / numHosts
	for host, count := range counts {
		// Random uniform assignment of 100k keys over 10 hosts keeps every
		// host well within 10% of the mean.
		assert.InDeltaf(t, expected, count, 0.1*float64(expected),
			"host %s owns %d keys, expected close to %d", host, count, expected)
	}
}

func TestMinimalMovementOnLeave(t *testing.T) {
	t.Parallel()

	const numHosts = 10
	const numKeys = 100_000
	removed := hostN(3)

	before := New(hosts(numHosts))
	after := New(append(hosts(numHosts)[:3], hosts(numHosts)[4:]...))

	moved := 0
	for i := range numKeys {
		key := "actor-" + strconv.Itoa(i)
		ownerBefore, ok := before.Lookup(key)
		require.True(t, ok)
		ownerAfter, ok := after.Lookup(key)
		require.True(t, ok)

		if ownerBefore == removed {
			moved++
			assert.NotEqual(t, removed, ownerAfter)
		} else {
			// A key not owned by the removed host must not move: its
			// remaining host scores are unchanged.
			assert.Equal(t, ownerBefore, ownerAfter)
		}
	}

	// Only the removed host's share moved, roughly 1/numHosts of all keys.
	expected := numKeys / numHosts
	assert.InDelta(t, expected, moved, 0.1*float64(expected))
}

func TestMinimalMovementOnJoin(t *testing.T) {
	t.Parallel()

	const numHosts = 9
	const numKeys = 100_000
	joined := hostN(100)

	before := New(hosts(numHosts))
	after := New(append(hosts(numHosts), joined))

	moved := 0
	for i := range numKeys {
		key := "actor-" + strconv.Itoa(i)
		ownerBefore, ok := before.Lookup(key)
		require.True(t, ok)
		ownerAfter, ok := after.Lookup(key)
		require.True(t, ok)

		if ownerBefore != ownerAfter {
			moved++
			// Every moved key must have moved to the new host, never
			// between existing hosts.
			assert.Equal(t, joined, ownerAfter)
		}
	}

	// The new host takes over roughly 1/(numHosts+1) of all keys.
	expected := numKeys / (numHosts + 1)
	assert.InDelta(t, expected, moved, 0.1*float64(expected))
}

func BenchmarkLookup(b *testing.B) {
	for _, n := range []int{3, 10, 50, 200} {
		b.Run(strconv.Itoa(n)+"-hosts", func(b *testing.B) {
			table := New(hosts(n))
			b.ResetTimer()
			for i := 0; b.Loop(); i++ {
				table.Lookup("actor-" + strconv.Itoa(i%10_000))
			}
		})
	}
}

// BenchmarkNew measures table construction, which runs on the placement
// leader and on every sidecar for each actor type whose membership changed.
// Per-type dissemination makes this the hot path on a membership change:
// where a vnode ring sorts hosts*replicationFactor entries, a rendezvous
// table sorts one entry per host.
func BenchmarkNew(b *testing.B) {
	for _, n := range []int{3, 10, 50, 200, 1000} {
		b.Run(strconv.Itoa(n)+"-hosts", func(b *testing.B) {
			hostSet := hosts(n)
			b.ResetTimer()
			for b.Loop() {
				New(hostSet)
			}
		})
	}
}
