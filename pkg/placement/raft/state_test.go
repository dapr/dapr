// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDaprHostMemberState(t *testing.T) {
	// act
	s := newDaprHostMemberState()

	// assert
	assert.Equal(t, uint64(0), s.Index())
	assert.Equal(t, 0, len(s.Members()))
	assert.Equal(t, 0, len(s.hashingTableMap()))
}

func TestClone(t *testing.T) {
	// arrange
	s := newDaprHostMemberState()
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
	s := newDaprHostMemberState()

	t.Run("add new actor member", func(t *testing.T) {
		// act
		updated := s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			AppID:     "FakeID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
		})

		// assert
		assert.Equal(t, 1, len(s.Members()))
		assert.Equal(t, 2, len(s.hashingTableMap()))
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
		assert.Equal(t, 2, len(s.Members()))
		assert.Equal(t, 2, len(s.hashingTableMap()))
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
		assert.Equal(t, 2, len(s.Members()))
		assert.True(t, updated)
		assert.Equal(t, 1, len(s.Members()[testMember.Name].Entities))
		assert.Equal(t, 3, len(s.hashingTableMap()), "this doesn't delete empty consistent hashing table")
	})
}

func TestRemoveMember(t *testing.T) {
	// arrange
	s := newDaprHostMemberState()

	t.Run("remove member and clean up consistent hashing table", func(t *testing.T) {
		// act
		updated := s.upsertMember(&DaprHostMember{
			Name:     "127.0.0.1:8080",
			AppID:    "FakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		})

		// assert
		assert.Equal(t, 1, len(s.Members()))
		assert.True(t, updated)
		assert.Equal(t, 2, len(s.hashingTableMap()))

		// act
		updated = s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})

		// assert
		assert.Equal(t, 0, len(s.Members()))
		assert.True(t, updated)
		assert.Equal(t, 0, len(s.hashingTableMap()))
	})

	t.Run("no table update required", func(t *testing.T) {
		// act
		updated := s.removeMember(&DaprHostMember{
			Name: "127.0.0.1:8080",
		})

		// assert
		assert.Equal(t, 0, len(s.Members()))
		assert.False(t, updated)
		assert.Equal(t, 0, len(s.hashingTableMap()))
	})
}

func TestUpdateHashingTable(t *testing.T) {
	// each subtest has dependency on the state

	// arrange
	s := newDaprHostMemberState()

	t.Run("add new hashing table per actor types", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:     "127.0.0.1:8080",
			AppID:    "FakeID",
			Entities: []string{"actorTypeOne", "actorTypeTwo"},
		}

		// act
		s.updateHashingTables(testMember)

		assert.Equal(t, 2, len(s.hashingTableMap()))
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

		assert.Equal(t, 3, len(s.hashingTableMap()))
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

	s := newDaprHostMemberState()
	for _, tc := range testcases {
		testMember.Name = tc.name
		s.updateHashingTables(testMember)
	}

	// act
	for _, tc := range testcases {
		t.Run("remove host "+tc.name, func(t *testing.T) {
			testMember.Name = tc.name
			s.removeHashingTables(testMember)

			assert.Equal(t, tc.totalTable, len(s.hashingTableMap()))
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
	assert.Equal(t, 0, len(s.hashingTableMap()))

	// act
	s.restoreHashingTables()

	// assert
	assert.Equal(t, 2, len(s.hashingTableMap()))
}
