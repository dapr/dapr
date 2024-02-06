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
	"testing"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	fsm := newFSM(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
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
	require.NoError(t, err)
	snap.Persist(fakeSink)

	// assert
	restoredState := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	err = restoredState.restore(buf)
	require.NoError(t, err)

	expectedMember := fsm.State().Members()[testMember.Name]
	restoredMember := restoredState.Members()[testMember.Name]
	assert.Equal(t, fsm.State().Index(), restoredState.Index())
	assert.Equal(t, expectedMember.Name, restoredMember.Name)
	assert.Equal(t, expectedMember.AppID, restoredMember.AppID)
	assert.EqualValues(t, expectedMember.Entities, restoredMember.Entities)
}
