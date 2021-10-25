// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"bytes"
	"io"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
)

func TestFSMApply(t *testing.T) {
	fsm := newFSM()

	t.Run("upsertMember", func(t *testing.T) {
		cmdLog, err := makeRaftLogCommand(MemberUpsert, DaprHostMember{
			Name:     "127.0.0.1:3030",
			AppID:    "fakeAppID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		})

		assert.NoError(t, err)

		raftLog := &raft.Log{
			Index: 1,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  cmdLog,
		}

		resp := fsm.Apply(raftLog)
		updated, ok := resp.(bool)

		assert.True(t, ok)
		assert.True(t, updated)
		assert.Equal(t, uint64(1), fsm.state.TableGeneration())
		assert.Equal(t, 1, len(fsm.state.Members()))
	})

	t.Run("removeMember", func(t *testing.T) {
		cmdLog, err := makeRaftLogCommand(MemberRemove, DaprHostMember{
			Name: "127.0.0.1:3030",
		})

		assert.NoError(t, err)

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
		assert.Equal(t, 0, len(fsm.state.Members()))
	})
}

func TestRestore(t *testing.T) {
	// arrange
	fsm := newFSM()

	s := newDaprHostMemberState()
	s.upsertMember(&DaprHostMember{
		Name:     "127.0.0.1:8080",
		AppID:    "FakeID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	})
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	err := s.persist(buf)
	assert.NoError(t, err)

	// act
	err = fsm.Restore(io.NopCloser(buf))

	// assert
	assert.NoError(t, err)
	assert.Equal(t, 1, len(fsm.State().Members()))
	assert.Equal(t, 2, len(fsm.State().hashingTableMap()))
}

func TestPlacementState(t *testing.T) {
	fsm := newFSM()
	m := DaprHostMember{
		Name:     "127.0.0.1:3030",
		AppID:    "fakeAppID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	}
	cmdLog, err := makeRaftLogCommand(MemberUpsert, m)
	assert.NoError(t, err)

	fsm.Apply(&raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  cmdLog,
	})

	newTable := fsm.PlacementState()
	assert.Equal(t, "1", newTable.Version)
	assert.Equal(t, 2, len(newTable.Entries))
}
