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

package inflight

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/actors/api"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func tables(entries map[string][]string) *schedulerv1pb.PlacementTables {
	t := &schedulerv1pb.PlacementTables{
		HashAlgorithm: schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_RENDEZVOUS,
		Entries:       make(map[string]*schedulerv1pb.PlacementTable),
	}
	for actorType, addrs := range entries {
		hosts := make(map[string]*schedulerv1pb.PlacementHost, len(addrs))
		for _, addr := range addrs {
			hosts[addr] = &schedulerv1pb.PlacementHost{Address: addr, AppId: "app-" + addr}
		}
		t.Entries[actorType] = &schedulerv1pb.PlacementTable{Hosts: hosts}
	}
	return t
}

func TestMerge(t *testing.T) {
	t.Parallel()

	t.Run("installs and merges partial tables", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "10.0.0.1", Port: "50002"})

		changed, err := i.Merge(tables(map[string][]string{
			"t1": {"10.0.0.1:50002"},
			"t2": {"10.0.0.2:50002"},
		}), map[string]uint64{"t1": 1, "t2": 1})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"t1", "t2"}, changed)

		// A partial merge for t2 leaves t1 untouched.
		changed, err = i.Merge(tables(map[string][]string{
			"t2": {"10.0.0.2:50002", "10.0.0.3:50002"},
		}), map[string]uint64{"t2": 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"t2"}, changed)

		resp, err := i.resolve(&api.LookupActorRequest{ActorType: "t1", ActorID: "a"})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.1:50002", resp.Address)
		assert.Equal(t, "app-10.0.0.1:50002", resp.AppID)
		assert.True(t, resp.Local)

		assert.True(t, i.HasTables([]string{"t1", "t2"}))
		assert.False(t, i.HasTables([]string{"t1", "t3"}))
	})

	t.Run("identical table is not a change", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "h", Port: "1"})

		_, err := i.Merge(tables(map[string][]string{"t1": {"a:1", "b:1"}}), map[string]uint64{"t1": 1})
		require.NoError(t, err)

		changed, err := i.Merge(tables(map[string][]string{"t1": {"b:1", "a:1"}}), map[string]uint64{"t1": 2})
		require.NoError(t, err)
		assert.Empty(t, changed)
	})

	t.Run("empty hosts removes the type", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "h", Port: "1"})

		_, err := i.Merge(tables(map[string][]string{"t1": {"a:1"}}), map[string]uint64{"t1": 1})
		require.NoError(t, err)

		changed, err := i.Merge(tables(map[string][]string{"t1": {}}), map[string]uint64{"t1": 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"t1"}, changed)

		_, err = i.resolve(&api.LookupActorRequest{ActorType: "t1", ActorID: "a"})
		require.Error(t, err)
	})

	t.Run("version regression errors, reset clears it", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "h", Port: "1"})

		_, err := i.Merge(tables(map[string][]string{"t1": {"a:1"}}), map[string]uint64{"t1": 5})
		require.NoError(t, err)

		_, err = i.Merge(tables(map[string][]string{"t1": {"b:1"}}), map[string]uint64{"t1": 3})
		require.ErrorContains(t, err, "version regression")

		// After a reconnect versions restart; the same lower version is fine.
		i.ResetSession()
		_, err = i.Merge(tables(map[string][]string{"t1": {"b:1"}}), map[string]uint64{"t1": 3})
		require.NoError(t, err)
	})

	t.Run("session reset drops all tables so a stale type cannot survive reconnect", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "10.0.0.1", Port: "50002"})

		_, err := i.Merge(tables(map[string][]string{
			"t1": {"10.0.0.1:50002"},
			"t2": {"10.0.0.2:50002"},
		}), map[string]uint64{"t1": 1, "t2": 1})
		require.NoError(t, err)

		// Stream loss ends the session. The reconnect snapshot contains only
		// t1: t2 was deleted while disconnected and sends no tombstone, so
		// only session-scoped state keeps it from surviving.
		i.ResetSession()
		_, err = i.Merge(tables(map[string][]string{
			"t1": {"10.0.0.1:50002"},
		}), map[string]uint64{"t1": 1})
		require.NoError(t, err)

		_, err = i.resolve(&api.LookupActorRequest{ActorType: "t2", ActorID: "a"})
		require.Error(t, err)
		_, err = i.resolve(&api.LookupActorRequest{ActorType: "t1", ActorID: "a"})
		require.NoError(t, err)
	})

	t.Run("unknown hash algorithm errors", func(t *testing.T) {
		t.Parallel()
		i := New(Options{Hostname: "h", Port: "1"})

		in := tables(map[string][]string{"t1": {"a:1"}})
		in.HashAlgorithm = schedulerv1pb.HashAlgorithm_HASH_ALGORITHM_UNKNOWN
		_, err := i.Merge(in, nil)
		require.ErrorContains(t, err, "unsupported placement hash algorithm")
	})
}
