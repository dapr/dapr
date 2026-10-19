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
	"slices"
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
