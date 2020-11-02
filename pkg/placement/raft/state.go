// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"time"

	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/google/go-cmp/cmp"
)

// DaprHostMember represents Dapr runtime host member, which can be
// actor service host or normal host.
type DaprHostMember struct {
	// Name is the unique name of Dapr runtime host.
	Name string
	// AppID is Dapr runtime app ID.
	AppID string
	// Entities is the list of Actor Types which this Dapr runtime supports.
	Entities []string

	// CreatedAt is the time when this host is first added.
	CreatedAt time.Time
	// UpdatedAt is the last time when this host member info is updated.
	UpdatedAt time.Time
}

// DaprHostMemberState is the state to store Dapr runtime host and
// consistent hashing tables.
type DaprHostMemberState struct {
	// Index is the index number of raft log.
	Index uint64
	// Members includes Dapr runtime hosts.
	Members map[string]*DaprHostMember

	// hashingTableMap is the map for storing consistent hashing data
	// per Actor types.
	hashingTableMap map[string]*hashing.Consistent
}

func newDaprHostMemberState() *DaprHostMemberState {
	return &DaprHostMemberState{
		Index:           0,
		Members:         map[string]*DaprHostMember{},
		hashingTableMap: map[string]*hashing.Consistent{},
	}
}

func (s *DaprHostMemberState) clone() *DaprHostMemberState {
	newMembers := &DaprHostMemberState{
		Index:           s.Index,
		Members:         map[string]*DaprHostMember{},
		hashingTableMap: nil,
	}
	for k, v := range s.Members {
		m := &DaprHostMember{
			Name:      v.Name,
			AppID:     v.AppID,
			Entities:  make([]string, len(v.Entities)),
			CreatedAt: v.CreatedAt,
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

func (s *DaprHostMemberState) upsertMember(host *DaprHostMember) error {
	now := time.Now().UTC()

	if m, ok := s.Members[host.Name]; ok {
		if m.AppID == host.AppID && m.Name == host.Name && cmp.Equal(m.Entities, host.Entities) {
			m.UpdatedAt = now
			return nil
		}
		s.removeHashingTables(m)
	}

	s.Members[host.Name] = &DaprHostMember{
		Name:     host.Name,
		AppID:    host.AppID,
		Entities: make([]string, len(host.Entities)),

		CreatedAt: now,
		UpdatedAt: now,
	}
	copy(s.Members[host.Name].Entities, host.Entities)

	s.updateHashingTables(s.Members[host.Name])

	return nil
}

func (s *DaprHostMemberState) removeMember(host *DaprHostMember) error {
	if m, ok := s.Members[host.Name]; ok {
		s.removeHashingTables(m)
		delete(s.Members, host.Name)
	}

	return nil
}

func (s *DaprHostMemberState) restoreHashingTables() {
	if s.hashingTableMap == nil {
		s.hashingTableMap = map[string]*hashing.Consistent{}
	}

	for _, m := range s.Members {
		s.updateHashingTables(m)
	}
}
