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
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func host(addr, appID string, types ...string) *schedulerv1pb.ActorHost {
	return &schedulerv1pb.ActorHost{
		Address:    addr,
		AppId:      appID,
		Namespace:  "ns",
		ActorTypes: types,
	}
}

func TestSet(t *testing.T) {
	t.Parallel()

	t.Run("new host returns its types as changed", func(t *testing.T) {
		t.Parallel()
		s := New()
		assert.ElementsMatch(t, []string{"t1", "t2"}, s.Set(1, host("a:1", "app", "t2", "t1")))
		assert.True(t, s.Has(1))
	})

	t.Run("new host with no types is not stored and changes nothing", func(t *testing.T) {
		t.Parallel()
		s := New()
		assert.Empty(t, s.Set(1, host("a:1", "app")))
		assert.False(t, s.Has(1))
	})

	t.Run("unchanged report changes nothing", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, host("a:1", "app", "t1"))
		assert.Empty(t, s.Set(1, host("a:1", "app", "t1")))
	})

	t.Run("added and removed types are changed", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, host("a:1", "app", "t1", "t2"))
		assert.ElementsMatch(t, []string{"t2", "t3"}, s.Set(1, host("a:1", "app", "t1", "t3")))
	})

	t.Run("report with no types removes host, all types changed", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, host("a:1", "app", "t1", "t2"))
		assert.ElementsMatch(t, []string{"t1", "t2"}, s.Set(1, host("a:1", "app")))
		assert.False(t, s.Has(1))
	})

	t.Run("changed address changes union of old and new types", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, host("a:1", "app", "t1", "t2"))
		assert.ElementsMatch(t, []string{"t1", "t2", "t3"}, s.Set(1, host("b:1", "app", "t2", "t3")))
	})
}

func TestDelete(t *testing.T) {
	t.Parallel()

	s := New()
	s.Set(1, host("a:1", "app", "t1", "t2"))
	assert.ElementsMatch(t, []string{"t1", "t2"}, s.Delete(1))
	assert.Empty(t, s.Delete(1))
}

func TestTables(t *testing.T) {
	t.Parallel()

	s := New()
	s.Set(1, host("a:1", "app-a", "t1", "t2"))
	s.Set(2, host("b:1", "app-b", "t2"))

	tables := s.Tables([]string{"t2", "t3"})
	require.NotNil(t, tables)
	assert.Equal(t, schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS, tables.GetHashAlgorithm())
	require.Len(t, tables.GetEntries(), 2)

	t2 := tables.GetEntries()["t2"]
	require.NotNil(t, t2)
	require.Len(t, t2.GetHosts(), 2)
	assert.Equal(t, "app-a", t2.GetHosts()["a:1"].GetAppId())
	assert.Equal(t, "app-b", t2.GetHosts()["b:1"].GetAppId())

	// A requested type with no hosts gets an empty entry, which removes the
	// type on the receiving side.
	t3 := tables.GetEntries()["t3"]
	require.NotNil(t, t3)
	assert.Empty(t, t3.GetHosts())

	// t1 was not requested: partial tables only carry requested types.
	assert.Nil(t, tables.GetEntries()["t1"])
}

func TestTypes(t *testing.T) {
	t.Parallel()

	s := New()
	assert.Empty(t, s.Types())
	s.Set(1, host("a:1", "app-a", "t2", "t1"))
	s.Set(2, host("b:1", "app-b", "t2", "t3"))
	assert.Equal(t, []string{"t1", "t2", "t3"}, s.Types())
}

func standbyHost(addr, appID string, types ...string) *schedulerv1pb.ActorHost {
	return &schedulerv1pb.ActorHost{
		Address:           addr,
		AppId:             appID,
		Namespace:         "ns",
		StandbyActorTypes: types,
	}
}

func tableHosts(tables *schedulerv1pb.PlacementTables, actorType string) []string {
	addrs := make([]string, 0, len(tables.GetEntries()[actorType].GetHosts()))
	for addr := range tables.GetEntries()[actorType].GetHosts() {
		addrs = append(addrs, addr)
	}
	return addrs
}

func TestStandby(t *testing.T) {
	t.Parallel()

	t.Run("standby hosts are placed while no host is active", func(t *testing.T) {
		t.Parallel()
		s := New()
		assert.Equal(t, []string{"wf"}, s.Set(1, standbyHost("a:1", "app", "wf")))
		assert.Equal(t, []string{"wf"}, s.Set(2, standbyHost("b:1", "app", "wf")))
		assert.True(t, s.Has(1))
		assert.Equal(t, []string{"wf"}, s.Types())
		assert.ElementsMatch(t, []string{"a:1", "b:1"}, tableHosts(s.Tables([]string{"wf"}), "wf"))
	})

	t.Run("an active host replaces the standby hosts", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, standbyHost("a:1", "app", "wf"))
		assert.Equal(t, []string{"wf"}, s.Set(2, host("b:1", "app", "wf")))
		assert.Equal(t, []string{"b:1"}, tableHosts(s.Tables([]string{"wf"}), "wf"))

		// A standby change does not move a table which has an active host.
		assert.Empty(t, s.Set(3, standbyHost("c:1", "app", "wf")))
		assert.Empty(t, s.Delete(3))

		// The last active host leaving places the standby hosts again.
		assert.Equal(t, []string{"wf"}, s.Delete(2))
		assert.Equal(t, []string{"a:1"}, tableHosts(s.Tables([]string{"wf"}), "wf"))
	})

	t.Run("an active host turning standby changes the table", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, standbyHost("a:1", "app", "wf"))
		s.Set(2, host("b:1", "app", "wf"))
		assert.Equal(t, []string{"wf"}, s.Set(2, standbyHost("b:1", "app", "wf")))
		assert.ElementsMatch(t, []string{"a:1", "b:1"}, tableHosts(s.Tables([]string{"wf"}), "wf"))
	})

	t.Run("a standby host changing address moves only a standby table", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, standbyHost("a:1", "app", "wf", "other"))
		s.Set(2, host("b:1", "app", "wf"))
		assert.Equal(t, []string{"other"}, s.Set(1, standbyHost("c:1", "app", "wf", "other")))
		assert.Equal(t, []string{"c:1"}, tableHosts(s.Tables([]string{"other"}), "other"))
	})

	t.Run("deleting the last standby host removes the type", func(t *testing.T) {
		t.Parallel()
		s := New()
		s.Set(1, standbyHost("a:1", "app", "wf"))
		assert.Equal(t, []string{"wf"}, s.Delete(1))
		assert.Empty(t, s.Types())
		assert.Empty(t, tableHosts(s.Tables([]string{"wf"}), "wf"))
	})
}

// benchTypes returns the workflow actor types of an app, plus extra.
func benchTypes(app string, extra ...string) []string {
	return append([]string{
		"dapr.internal.ns." + app + ".workflow",
		"dapr.internal.ns." + app + ".activity",
		"dapr.internal.ns." + app + ".retentioner",
	}, extra...)
}

// benchStore returns a store with n active hosts and m standby hosts, both in
// apps of ten replicas. An active host serves its app's workflow types and an
// actor type; a standby host is a daprd with the WorkflowsStandbyHosts flag
// whose app never connects a worker, the worst case, as its types are never
// served actively and so stay in the tables.
func benchStore(n, m int) *Store {
	s := New()
	for i := range n {
		app := "active-" + strconv.Itoa(i/10)
		//nolint:gosec
		s.Set(uint64(i), host("active-"+strconv.Itoa(i)+":1", app, benchTypes(app, "actor-"+app)...))
	}
	for j := range m {
		app := "standby-" + strconv.Itoa(j/10)
		//nolint:gosec
		s.Set(uint64(n+j), standbyHost("standby-"+strconv.Itoa(j)+":1", app, benchTypes(app)...))
	}
	return s
}

func benchSizes(b *testing.B, fn func(b *testing.B, n, m int)) {
	for _, n := range []int{10, 100} {
		for _, m := range []int{0, 100, 1000} {
			b.Run(fmt.Sprintf("active=%d/standby=%d", n, m), func(b *testing.B) {
				fn(b, n, m)
			})
		}
	}
}

// BenchmarkSetDelete measures a daprd connecting and leaving, which computes
// the types whose table changed for each.
func BenchmarkSetDelete(b *testing.B) {
	run := func(name string, newHost func(n, m int) *schedulerv1pb.ActorHost) {
		b.Run(name, func(b *testing.B) {
			benchSizes(b, func(b *testing.B, n, m int) {
				s := benchStore(n, m)
				//nolint:gosec
				idx := uint64(n + m)
				for b.Loop() {
					s.Set(idx, newHost(n, m))
					s.Delete(idx)
				}
			})
		})
	}

	run("active", func(int, int) *schedulerv1pb.ActorHost {
		return host("new:1", "new", benchTypes("new")...)
	})
	// A standby daprd of an app with no active host moves its tables.
	run("standby-new-app", func(int, int) *schedulerv1pb.ActorHost {
		return standbyHost("new:1", "new", benchTypes("new")...)
	})
	// A standby daprd of an app whose other standby daprds are placed.
	run("standby-standby-app", func(int, int) *schedulerv1pb.ActorHost {
		return standbyHost("new:1", "standby-0", benchTypes("standby-0")...)
	})
	// A standby daprd of an app with an active host leaves its tables.
	run("standby-active-app", func(int, int) *schedulerv1pb.ActorHost {
		return standbyHost("new:1", "active-0", benchTypes("active-0")...)
	})
}

// BenchmarkTables measures building the tables of one app's types, as a
// round's UPDATE does.
func BenchmarkTables(b *testing.B) {
	benchSizes(b, func(b *testing.B, n, m int) {
		s := benchStore(n, m)
		types := benchTypes("active-0")
		for b.Loop() {
			s.Tables(types)
		}
	})
}

// BenchmarkSnapshot measures building every table, as the snapshot pushed to
// each newly connected stream does.
func BenchmarkSnapshot(b *testing.B) {
	benchSizes(b, func(b *testing.B, n, m int) {
		s := benchStore(n, m)
		for b.Loop() {
			s.Tables(s.Types())
		}
	})
}
