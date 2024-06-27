/*
Copyright 2021 The Dapr Authors
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

package raft

import (
	"bytes"
	"io"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFSMApply(t *testing.T) {
	fsm := newFSM(DaprHostMemberStateConfig{
		replicationFactor: 100,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	t.Run("upsertMember", func(t *testing.T) {
		cmdLog, err := makeRaftLogCommand(MemberUpsert, DaprHostMember{
			Name:      "127.0.0.1:3030",
			Namespace: "ns1",
			AppID:     "fakeAppID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
		})

		require.NoError(t, err)

		raftLog := &raft.Log{
			Index: 1,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  cmdLog,
		}

		resp := fsm.Apply(raftLog)
		updated, ok := resp.(bool)

		require.True(t, ok)
		require.True(t, updated)
		require.Equal(t, uint64(1), fsm.state.TableGeneration())

		require.Equal(t, 1, fsm.state.NamespaceCount())

		var containsNamespace bool
		fsm.state.ForEachNamespace(func(ns string, _ *daprNamespace) bool {
			containsNamespace = ns == "ns1"
			return true
		})
		require.True(t, containsNamespace)

		fsm.state.lock.RLock()
		defer fsm.state.lock.RUnlock()

		members, err := fsm.state.members("ns1")

		require.NoError(t, err)

		assert.Len(t, members, 1)
	})

	t.Run("removeMember", func(t *testing.T) {
		cmdLog, err := makeRaftLogCommand(MemberRemove, DaprHostMember{
			Name:      "127.0.0.1:3030",
			Namespace: "ns1",
		})

		require.NoError(t, err)

		raftLog := &raft.Log{
			Index: 2,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  cmdLog,
		}

		resp := fsm.Apply(raftLog)
		updated, ok := resp.(bool)

		assert.True(t, ok)
		assert.True(t, updated)
		assert.Equal(t, uint64(2), fsm.state.TableGeneration())
		require.Equal(t, 0, fsm.state.MemberCountInNamespace("ns1"))
	})
}

func TestRestore(t *testing.T) {
	fsm := newFSM(DaprHostMemberStateConfig{
		replicationFactor: 100,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 100,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	s.upsertMember(&DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
	})
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	err := s.persist(buf)
	require.NoError(t, err)

	err = fsm.Restore(io.NopCloser(buf))
	require.NoError(t, err)

	require.Equal(t, 1, fsm.state.MemberCountInNamespace("ns1"))

	hashingTable, err := fsm.State().hashingTableMap("ns1")
	require.NoError(t, err)
	require.Len(t, hashingTable, 2)
}

func TestPlacementStateWithVirtualNodes(t *testing.T) {
	fsm := newFSM(DaprHostMemberStateConfig{
		replicationFactor: 5,
	})

	m := DaprHostMember{
		Name:      "127.0.0.1:3030",
		Namespace: "ns1",
		AppID:     "fakeAppID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
		APILevel:  10,
	}
	cmdLog, err := makeRaftLogCommand(MemberUpsert, m)
	require.NoError(t, err)

	fsm.Apply(&raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  cmdLog,
	})

	newTable := fsm.PlacementState(true, "ns1")
	assert.Equal(t, "1", newTable.GetVersion())
	assert.Len(t, newTable.GetEntries(), 2)
	assert.Equal(t, int64(5), newTable.GetReplicationFactor())

	for _, host := range newTable.GetEntries() {
		assert.Len(t, host.GetHosts(), 5)
		assert.Len(t, host.GetSortedSet(), 5)
		assert.Len(t, host.GetLoadMap(), 1)
		assert.Contains(t, host.GetLoadMap(), "127.0.0.1:3030")
	}
}

func TestPlacementState(t *testing.T) {
	fsm := newFSM(DaprHostMemberStateConfig{
		replicationFactor: 5,
	})
	m := DaprHostMember{
		Name:      "127.0.0.1:3030",
		Namespace: "ns1",
		AppID:     "fakeAppID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
	}
	cmdLog, err := makeRaftLogCommand(MemberUpsert, m)
	require.NoError(t, err)

	fsm.Apply(&raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  cmdLog,
	})

	newTable := fsm.PlacementState(false, "ns1")
	assert.Equal(t, "1", newTable.GetVersion())
	assert.Len(t, newTable.GetEntries(), 2)
	assert.Equal(t, int64(5), newTable.GetReplicationFactor())

	for _, host := range newTable.GetEntries() {
		assert.Empty(t, host.GetHosts())
		assert.Empty(t, host.GetSortedSet())
		assert.Len(t, host.GetLoadMap(), 1)
		assert.Contains(t, host.GetLoadMap(), "127.0.0.1:3030")
	}
}
