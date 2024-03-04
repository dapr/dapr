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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDaprHostMemberState(t *testing.T) {
	// act
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	// assert
	assert.Equal(t, uint64(0), s.Index())
	assert.Empty(t, s.Members())
	assert.Empty(t, s.hashingTableMap())
}

func TestClone(t *testing.T) {
	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	s.upsertMember(&DaprHostMember{
		Name:     "127.0.0.1:8080",
		AppID:    "FakeID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	})

	// act
	newState := s.clone()

	// assert
	assert.NotSame(t, s, newState)
	assert.Nil(t, newState.hashingTableMap())
	assert.Equal(t, s.Index(), newState.Index())
	assert.EqualValues(t, s.Members(), newState.Members())
}

func TestUpsertMember(t *testing.T) {
	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	t.Run("add new actor member", func(t *testing.T) {
		// act
		updated := s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
		})

		// assert
		assert.Len(t, s.Members(), 1)
		assert.Len(t, s.hashingTableMap(), 2)
		assert.True(t, updated)
	})

	t.Run("no hashing table update required", func(t *testing.T) {
		// act
		updated := s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8081",
			AppID:     "FakeID_2",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
		})

		// assert
		assert.Len(t, s.Members(), 2)
		assert.Len(t, s.hashingTableMap(), 2)
		assert.True(t, updated)

		// act
		updated = s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8081",
			AppID:     "FakeID_2",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 2,
		})

		// assert
		assert.False(t, updated)
	})

	t.Run("non actor host", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID",
			Entities:  []string{},
			UpdatedAt: 100,
		}

		// act
		updated := s.upsertMember(testMember)
		// assert
		assert.False(t, updated)
	})

	t.Run("update existing actor member", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID",
			Entities:  []string{"actorTypeThree"},
			UpdatedAt: 100,
		}

		// act
		//
		// this tries to update the existing actor members.
		// it will delete empty consistent hashing table.
		updated := s.upsertMember(testMember)

		// assert
		assert.Len(t, s.Members(), 2)
		assert.True(t, updated)
		assert.Len(t, s.Members()[testMember.Name].Entities, 1)
		assert.Len(t, s.hashingTableMap(), 3, "this doesn't delete empty consistent hashing table")
	})
}

func TestRemoveMember(t *testing.T) {
	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	t.Run("remove member and clean up consistent hashing table", func(t *testing.T) {
		// act
		updated := s.upsertMember(&DaprHostMember{
			Name:     "127.0.0.1:8080",
			AppID:    "FakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		})

		// assert
		assert.Len(t, s.Members(), 1)
		assert.True(t, updated)
		assert.Len(t, s.hashingTableMap(), 2)

		// act
		updated = s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})

		// assert
		assert.Empty(t, s.Members())
		assert.True(t, updated)
		assert.Empty(t, s.hashingTableMap())
	})

	t.Run("no table update required", func(t *testing.T) {
		// act
		updated := s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})

		// assert
		assert.Empty(t, s.Members())
		assert.False(t, updated)
		assert.Empty(t, s.hashingTableMap())
	})
}

func TestUpdateHashingTable(t *testing.T) {
	// each subtest has dependency on the state

	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	t.Run("add new hashing table per actor types", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:     "127.0.0.1:8080",
			AppID:    "FakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		}

		// act
		s.updateHashingTables(testMember)

		assert.Len(t, s.hashingTableMap(), 2)
		for _, ent := range testMember.Entities {
			assert.NotNil(t, s.hashingTableMap()[ent])
		}
	})

	t.Run("update new hashing table per actor types", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:     "127.0.0.1:8080",
			AppID:    "FakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo", "actorTypeThree"},
		}

		// act
		s.updateHashingTables(testMember)

		assert.Len(t, s.hashingTableMap(), 3)
		for _, ent := range testMember.Entities {
			assert.NotNil(t, s.hashingTableMap()[ent])
		}
	})
}

func TestRemoveHashingTable(t *testing.T) {
	// each subtest has dependency on the state

	// arrange
	testMember := &DaprHostMember{
		Name:     "fakeName",
		AppID:    "fakeID",
		Entities: []string{"actorTypeOne", "actorTypeTwo"},
	}

	testcases := []struct {
		name       string
		totalTable int
	}{
		{"127.0.0.1:8080", 2},
		{"127.0.0.1:8081", 0},
	}

	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	for _, tc := range testcases {
		testMember.Name = tc.name
		s.updateHashingTables(testMember)
	}

	// act
	for _, tc := range testcases {
		t.Run("remove host "+tc.name, func(t *testing.T) {
			testMember.Name = tc.name
			s.removeHashingTables(testMember)

			assert.Len(t, s.hashingTableMap(), tc.totalTable)
		})
	}
}

func TestRestoreHashingTables(t *testing.T) {
	// arrange
	testnames := []string{
		"127.0.0.1:8080",
		"127.0.0.1:8081",
	}

	s := &DaprHostMemberState{
		data: DaprHostMemberStateData{
			Index:           0,
			Members:         map[string]*DaprHostMember{},
			hashingTableMap: nil,
		},
	}
	for _, tn := range testnames {
		s.lock.Lock()
		s.data.Members[tn] = &DaprHostMember{
			Name:     tn,
			AppID:    "fakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		}
		s.lock.Unlock()
	}
	assert.Empty(t, s.hashingTableMap())

	// act
	s.restoreHashingTables()

	// assert
	assert.Len(t, s.hashingTableMap(), 2)
}

func TestUpdateAPILevel(t *testing.T) {
	t.Run("no min nor max api levels arguments", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
			minAPILevel:       0,
			maxAPILevel:       100,
		})

		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  10,
		})
		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8081",
			AppID:     "FakeID2",
			Entities:  []string{"actorTypeThree", "actorTypeFour"},
			UpdatedAt: 2,
			APILevel:  20,
		})
		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8082",
			AppID:     "FakeID3",
			Entities:  []string{"actorTypeFive"},
			UpdatedAt: 3,
			APILevel:  30,
		})

		assert.Equal(t, uint32(10), s.data.APILevel)

		s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})
		assert.Equal(t, uint32(20), s.data.APILevel)

		s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8081",
		})
		assert.Equal(t, uint32(30), s.data.APILevel)

		s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8082",
		})
		assert.Equal(t, uint32(30), s.data.APILevel)
	})

	t.Run("min api levels set", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
			minAPILevel:       20,
			maxAPILevel:       100,
		})
		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  10,
		})
		assert.Equal(t, uint32(20), s.data.APILevel)

		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8081",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 2,
			APILevel:  30,
		})
		assert.Equal(t, uint32(20), s.data.APILevel)

		s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})
		assert.Equal(t, uint32(30), s.data.APILevel)
	})

	t.Run("max api levels set", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
			minAPILevel:       0,
			maxAPILevel:       20,
		})
		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  30,
		})

		assert.Equal(t, uint32(20), s.data.APILevel)
	})
}
