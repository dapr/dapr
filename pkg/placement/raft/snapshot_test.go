// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"bytes"
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
)

type MockSnapShotSink struct {
	*bytes.Buffer
	cancel bool
}

func (m *MockSnapShotSink) ID() string {
	return "Mock"
}

func (m *MockSnapShotSink) Cancel() error {
	m.cancel = true
	return nil
}

func (m *MockSnapShotSink) Close() error {
	return nil
}

func TestPersist(t *testing.T) {
	// arrange
	fsm := newFSM()
	testMember := DaprHostMember{
		Name:     "127.0.0.1:3030",
		AppID:    "fakeAppID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	}
	cmdLog, _ := makeRaftLogCommand(MemberUpsert, testMember)
	raftLog := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  cmdLog,
	}
	fsm.Apply(raftLog)
	buf := bytes.NewBuffer(nil)
	fakeSink := &MockSnapShotSink{buf, false}

	// act
	snap, err := fsm.Snapshot()
	assert.NoError(t, err)
	snap.Persist(fakeSink)

	// assert
	restoredState := newDaprHostMemberState()
	err = restoredState.restore(buf)
	assert.NoError(t, err)

	expectedMember := fsm.State().Members()[testMember.Name]
	restoredMember := restoredState.Members()[testMember.Name]
	assert.Equal(t, fsm.State().Index(), restoredState.Index())
	assert.Equal(t, expectedMember.Name, restoredMember.Name)
	assert.Equal(t, expectedMember.AppID, restoredMember.AppID)
	assert.EqualValues(t, expectedMember.Entities, restoredMember.Entities)
}
