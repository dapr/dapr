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
