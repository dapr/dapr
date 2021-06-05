// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"github.com/google/go-cmp/cmp"

	"github.com/dapr/dapr/pkg/placement/hashing"
)

// DaprHostMember represents Dapr runtime actor host member which serve actor types.
type DaprHostMember struct {
	// Name is the unique name of Dapr runtime host.
	Name string
	// AppID is Dapr runtime app ID.
	AppID string
	// Entities is the list of Actor Types which this Dapr runtime supports.
	Entities []string

	// UpdatedAt is the last time when this host member info is updated.
	UpdatedAt int64
}

// DaprHostMemberState is the state to store Dapr runtime host and
// consistent hashing tables.
type DaprHostMemberState struct {
	// Index is the index number of raft log.
	Index uint64
	// Members includes Dapr runtime hosts.
	Members map[string]*DaprHostMember

	// TableGeneration is the generation of hashingTableMap.
	// This is increased whenever hashingTableMap is updated.
	TableGeneration uint64

	// hashingTableMap is the map for storing consistent hashing data
	// per Actor types. This will be generated when log entries are replayed.
	// While snapshotting the state, this member will not be saved. Instead,
	// hashingTableMap will be recovered in snapshot recovery process.
	hashingTableMap map[string]*hashing.Consistent
}

func newDaprHostMemberState() *DaprHostMemberState {
	return &DaprHostMemberState{
		Index:           0,
		TableGeneration: 0,
		Members:         map[string]*DaprHostMember{},
		hashingTableMap: map[string]*hashing.Consistent{},
	}
}

func (s *DaprHostMemberState) clone() *DaprHostMemberState {
	newMembers := &DaprHostMemberState{
		Index:           s.Index,
		TableGeneration: s.TableGeneration,
		Members:         map[string]*DaprHostMember{},
		hashingTableMap: nil,
	}
	for k, v := range s.Members {
		m := &DaprHostMember{
			Name:      v.Name,
			AppID:     v.AppID,
			Entities:  make([]string, len(v.Entities)),
			UpdatedAt: v.UpdatedAt,
		}
		copy(m.Entities, v.Entities)
		newMembers.Members[k] = m
	}
	return newMembers
}

func (s *DaprHostMemberState) updateHashingTables(host *DaprHostMember) {
	for _, e := range host.Entities {
		if _, ok := s.hashingTableMap[e]; !ok {
			s.hashingTableMap[e] = hashing.NewConsistentHash()
		}

		s.hashingTableMap[e].Add(host.Name, host.AppID, 0)
	}
}

func (s *DaprHostMemberState) removeHashingTables(host *DaprHostMember) {
	for _, e := range host.Entities {
		if t, ok := s.hashingTableMap[e]; ok {
			t.Remove(host.Name)

			// if no dedicated actor service instance for the particular actor type,
			// we must delete consistent hashing table to avoid the memory leak.
			if len(t.Hosts()) == 0 {
				delete(s.hashingTableMap, e)
			}
		}
	}
}

// upsertMember upserts member host info to the FSM state and returns true
// if the hashing table update happens.
func (s *DaprHostMemberState) upsertMember(host *DaprHostMember) bool {
	if !s.isActorHost(host) {
		return false
	}

	if m, ok := s.Members[host.Name]; ok {
		// No need to update consistent hashing table if the same dapr host member exists
		if m.AppID == host.AppID && m.Name == host.Name && cmp.Equal(m.Entities, host.Entities) {
			m.UpdatedAt = host.UpdatedAt
			return false
		}

		// Remove hashing table because the existing member is invalid
		// and needs to be updated by new member info.
		s.removeHashingTables(m)
	}

	s.Members[host.Name] = &DaprHostMember{
		Name:      host.Name,
		AppID:     host.AppID,
		UpdatedAt: host.UpdatedAt,
	}

	// Update hashing table only when host reports actor types
	s.Members[host.Name].Entities = make([]string, len(host.Entities))
	copy(s.Members[host.Name].Entities, host.Entities)

	s.updateHashingTables(s.Members[host.Name])

	// Increase hashing table generation version. Runtime will compare the table generation
	// version with its own and then update it if it is new.
	s.TableGeneration++

	return true
}

// removeMember removes members from membership and update hashing table and returns true
// if hashing table update happens.
func (s *DaprHostMemberState) removeMember(host *DaprHostMember) bool {
	if m, ok := s.Members[host.Name]; ok {
		s.removeHashingTables(m)
		s.TableGeneration++
		delete(s.Members, host.Name)

		return true
	}

	return false
}

func (s *DaprHostMemberState) isActorHost(host *DaprHostMember) bool {
	return len(host.Entities) > 0
}

func (s *DaprHostMemberState) restoreHashingTables() {
	if s.hashingTableMap == nil {
		s.hashingTableMap = map[string]*hashing.Consistent{}
	}

	for _, m := range s.Members {
		s.updateHashingTables(m)
	}
}
