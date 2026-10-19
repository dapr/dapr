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
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func hostNames(t *v1pb.PlacementTables, entity string) []string {
	names := make([]string, 0, len(t.GetEntries()[entity].GetLoadMap()))
	for name := range t.GetEntries()[entity].GetLoadMap() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func TestPlacementTablesStandby(t *testing.T) {
	t.Run("standby hosts are placed while no host is active", func(t *testing.T) {
		s := New(Options{})
		assert.True(t, s.Set(1, &v1pb.Host{Name: "a", Id: "app", StandbyEntities: []string{"wf"}}))
		assert.True(t, s.Set(2, &v1pb.Host{Name: "b", Id: "app", StandbyEntities: []string{"wf"}}))

		assert.Equal(t, []string{"a", "b"}, hostNames(s.PlacementTables(1), "wf"))
	})

	t.Run("an active host replaces the standby hosts", func(t *testing.T) {
		s := New(Options{})
		s.Set(1, &v1pb.Host{Name: "a", Id: "app", StandbyEntities: []string{"wf"}})
		s.Set(2, &v1pb.Host{Name: "b", Id: "app", StandbyEntities: []string{"wf"}, Entities: []string{"other"}})
		assert.True(t, s.Set(3, &v1pb.Host{Name: "c", Id: "app", Entities: []string{"wf"}}))

		tables := s.PlacementTables(1)
		assert.Equal(t, []string{"c"}, hostNames(tables, "wf"))
		assert.Equal(t, []string{"b"}, hostNames(tables, "other"))

		// The active host turns standby: the standby hosts are placed again.
		assert.True(t, s.Set(3, &v1pb.Host{Name: "c", Id: "app", StandbyEntities: []string{"wf"}}))
		assert.Equal(t, []string{"a", "b", "c"}, hostNames(s.PlacementTables(2), "wf"))
	})

	t.Run("a host with only standby entities is stored", func(t *testing.T) {
		s := New(Options{})
		assert.True(t, s.Set(1, &v1pb.Host{Name: "a", Id: "app", StandbyEntities: []string{"wf"}}))
		assert.True(t, s.Has(1))
		assert.False(t, s.Set(1, &v1pb.Host{Name: "a", Id: "app", StandbyEntities: []string{"wf"}}))

		assert.True(t, s.Set(1, &v1pb.Host{Name: "a", Id: "app"}))
		assert.False(t, s.Has(1))
		assert.Empty(t, s.PlacementTables(1).GetEntries())
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
	s := New(Options{ReplicationFactor: 100})
	for i := range n {
		app := "active-" + strconv.Itoa(i/10)
		s.Set(uint64(i), &v1pb.Host{
			Name: app + "-" + strconv.Itoa(i), Id: app, Namespace: "ns",
			Entities: benchTypes(app, "actor-"+app),
		})
	}
	for j := range m {
		app := "standby-" + strconv.Itoa(j/10)
		s.Set(uint64(n+j), &v1pb.Host{
			Name: app + "-" + strconv.Itoa(j), Id: app, Namespace: "ns",
			StandbyEntities: benchTypes(app),
		})
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

// BenchmarkPlacementTables measures building the tables, which the
// disseminator does for every UPDATE it sends.
func BenchmarkPlacementTables(b *testing.B) {
	benchSizes(b, func(b *testing.B, n, m int) {
		s := benchStore(n, m)
		for b.Loop() {
			s.PlacementTables(1)
		}
	})
}

// BenchmarkSet measures a daprd connecting and leaving: its host report is
// stored, then replaced by an empty report.
func BenchmarkSet(b *testing.B) {
	b.Run("standby", func(b *testing.B) {
		benchSizes(b, func(b *testing.B, n, m int) {
			s := benchStore(n, m)
			for b.Loop() {
				s.Set(uint64(n+m), &v1pb.Host{Name: "new", Id: "new", Namespace: "ns", StandbyEntities: benchTypes("new")})
				s.Set(uint64(n+m), &v1pb.Host{Name: "new", Id: "new", Namespace: "ns"})
			}
		})
	})
	b.Run("active", func(b *testing.B) {
		benchSizes(b, func(b *testing.B, n, m int) {
			s := benchStore(n, m)
			for b.Loop() {
				s.Set(uint64(n+m), &v1pb.Host{Name: "new", Id: "new", Namespace: "ns", Entities: benchTypes("new")})
				s.Set(uint64(n+m), &v1pb.Host{Name: "new", Id: "new", Namespace: "ns"})
			}
		})
	})
}
