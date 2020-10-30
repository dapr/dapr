// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"bytes"
	"io/ioutil"
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
		err, ok := resp.(error)

		assert.False(t, ok)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(fsm.state.Members))
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
		err, ok := resp.(error)

		assert.False(t, ok)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(fsm.state.Members))
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
	data, err := marshalMsgPack(s)
	assert.NoError(t, err)
	buf := ioutil.NopCloser(bytes.NewBuffer(data))

	// act
	err = fsm.Restore(buf)

	// assert
	assert.NoError(t, err)
	assert.Equal(t, 1, len(fsm.State().Members))
	assert.Equal(t, 2, len(fsm.State().hashingTableMap))
}
