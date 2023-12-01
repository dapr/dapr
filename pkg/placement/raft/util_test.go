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
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureDir(t *testing.T) {
	testDir := "_testDir"
	t.Run("create dir successfully", func(t *testing.T) {
		err := ensureDir(testDir)
		require.NoError(t, err)
		err = os.Remove(testDir)
		require.NoError(t, err)
	})

	t.Run("ensure the existing directory", func(t *testing.T) {
		err := os.Mkdir(testDir, 0o700)
		require.NoError(t, err)
		err = ensureDir(testDir)
		require.NoError(t, err)
		err = os.Remove(testDir)
		require.NoError(t, err)
	})

	t.Run("fails to create dir", func(t *testing.T) {
		file, err := os.Create(testDir)
		require.NoError(t, err)
		file.Close()
		err = ensureDir(testDir)
		require.Error(t, err)
		err = os.Remove(testDir)
		require.NoError(t, err)
	})
}

func TestRaftAddressForID(t *testing.T) {
	raftAddressTests := []struct {
		in  []PeerInfo
		id  string
		out string
	}{
		{
			[]PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
				{ID: "node1", Address: "127.0.0.1:3031"},
			},
			"node0",
			"127.0.0.1:3030",
		}, {
			[]PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
			},
			"node1",
			"",
		},
	}

	for _, tt := range raftAddressTests {
		t.Run(fmt.Sprintf("find %s from %v", tt.id, tt.in), func(t *testing.T) {
			assert.Equal(t, tt.out, raftAddressForID(tt.id, tt.in))
		})
	}
}

func TestMarshalAndUnmarshalMsgpack(t *testing.T) {
	type testStruct struct {
		Name            string
		StringArrayList []string
		notSerialized   map[string]string
	}

	testObject := testStruct{
		Name:            "namevalue",
		StringArrayList: []string{"value1", "value2"},
		notSerialized: map[string]string{
			"key": "value",
		},
	}

	encoded, err := marshalMsgPack(testObject)
	require.NoError(t, err)

	var decoded testStruct
	err = unmarshalMsgPack(encoded, &decoded)
	require.NoError(t, err)

	assert.Equal(t, testObject.Name, decoded.Name)
	assert.Equal(t, testObject.StringArrayList, decoded.StringArrayList)
	assert.Nil(t, decoded.notSerialized)
}

func TestMakeRaftLogCommand(t *testing.T) {
	// arrange
	testMember := DaprHostMember{
		Name:     "127.0.0.1:3030",
		AppID:    "fakeAppID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	}

	// act
	cmdLog, _ := makeRaftLogCommand(MemberUpsert, testMember)

	// assert
	assert.Equal(t, uint8(MemberUpsert), cmdLog[0])
	unmarshaled := DaprHostMember{}
	unmarshalMsgPack(cmdLog[1:], &unmarshaled)
	assert.EqualValues(t, testMember, unmarshaled)
}
