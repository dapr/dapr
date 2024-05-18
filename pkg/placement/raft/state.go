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
	"io"
	"sync"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-msgpack/v2/codec"

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

	// Version of the Actor APIs supported by the Dapr runtime
	APILevel uint32
}

type DaprHostMemberStateData struct {
	// Index is the index number of raft log.
	Index uint64
	// Members includes Dapr runtime hosts.
	Members map[string]*DaprHostMember

	// TableGeneration is the generation of hashingTableMap.
	// This is increased whenever hashingTableMap is updated.
	TableGeneration uint64

	// Version of the actor APIs for the cluster
	APILevel uint32

	// hashingTableMap is the map for storing consistent hashing data
	// per Actor types. This will be generated when log entries are replayed.
	// While snapshotting the state, this member will not be saved. Instead,
	// hashingTableMap will be recovered in snapshot recovery process.
	hashingTableMap map[string]*hashing.Consistent
}

// DaprHostMemberState is the state to store Dapr runtime host and
// consistent hashing tables.
type DaprHostMemberState struct {
	lock sync.RWMutex

	config DaprHostMemberStateConfig
	data   DaprHostMemberStateData
}

type DaprHostMemberStateConfig struct {
	replicationFactor int64
	minAPILevel       uint32
	maxAPILevel       uint32
}

func newDaprHostMemberState(config DaprHostMemberStateConfig) *DaprHostMemberState {
	return &DaprHostMemberState{
		config: config,
		data: DaprHostMemberStateData{
			Members:         map[string]*DaprHostMember{},
			hashingTableMap: map[string]*hashing.Consistent{},
		},
	}
}

func (s *DaprHostMemberState) Index() uint64 {
	s.lock.RLock()
	defer s.lock.RUnlock()

	return s.data.Index
}

// APILevel returns the current API level of the cluster.
func (s *DaprHostMemberState) APILevel() uint32 {
	s.lock.RLock()
	defer s.lock.RUnlock()

	return s.data.APILevel
}

func (s *DaprHostMemberState) Members() map[string]*DaprHostMember {
	s.lock.RLock()
	defer s.lock.RUnlock()

	members := make(map[string]*DaprHostMember, len(s.data.Members))
	for k, v := range s.data.Members {
		members[k] = v
	}
	return members
}

func (s *DaprHostMemberState) TableGeneration() uint64 {
	s.lock.RLock()
	defer s.lock.RUnlock()

	return s.data.TableGeneration
}

// Internal function that updates the API level in the object.
// The API level can only be increased.
// Make sure you have a lock before calling this method.
func (s *DaprHostMemberState) updateAPILevel() {
	var observedMinLevel uint32
	for k := range s.data.Members {
		apiLevel := s.data.Members[k].APILevel
		if apiLevel <= 0 {
			apiLevel = 0
		}
		if observedMinLevel == 0 || observedMinLevel > apiLevel {
			observedMinLevel = apiLevel
		}
	}

	// Only enforce minAPILevel if value > 0
	// 0 is the default value of the struct.
	// -1 is the default value of the CLI flag.
	if s.config.minAPILevel >= uint32(0) && observedMinLevel < s.config.minAPILevel {
		observedMinLevel = s.config.minAPILevel
	}

	// Only enforce maxAPILevel if value > 0
	// 0 is the default value of the struct.
	// -1 is the default value of the CLI flag.
	if s.config.maxAPILevel >= uint32(0) && observedMinLevel > s.config.maxAPILevel {
		observedMinLevel = s.config.maxAPILevel
	}

	if observedMinLevel > s.data.APILevel {
		s.data.APILevel = observedMinLevel
	}
}

func (s *DaprHostMemberState) hashingTableMap() map[string]*hashing.Consistent {
	s.lock.RLock()
	defer s.lock.RUnlock()

	return s.data.hashingTableMap
}

func (s *DaprHostMemberState) clone() *DaprHostMemberState {
	s.lock.RLock()
	defer s.lock.RUnlock()

	newMembers := &DaprHostMemberState{
		config: s.config,
		data: DaprHostMemberStateData{
			Index:           s.data.Index,
			TableGeneration: s.data.TableGeneration,
			Members:         make(map[string]*DaprHostMember, len(s.data.Members)),
			APILevel:        s.data.APILevel,
		},
	}
	for k, v := range s.data.Members {
		m := &DaprHostMember{
			Name:      v.Name,
			AppID:     v.AppID,
			Entities:  make([]string, len(v.Entities)),
			UpdatedAt: v.UpdatedAt,
			APILevel:  v.APILevel,
		}
		copy(m.Entities, v.Entities)
		newMembers.data.Members[k] = m
	}
	return newMembers
}

// caller should holds lock.
func (s *DaprHostMemberState) updateHashingTables(host *DaprHostMember) {
	for _, e := range host.Entities {
		if _, ok := s.data.hashingTableMap[e]; !ok {
			s.data.hashingTableMap[e] = hashing.NewConsistentHash(s.config.replicationFactor)
		}

		s.data.hashingTableMap[e].Add(host.Name, host.AppID, 0)
	}
}

// caller should holds lock.
func (s *DaprHostMemberState) removeHashingTables(host *DaprHostMember) {
	for _, e := range host.Entities {
		if t, ok := s.data.hashingTableMap[e]; ok {
			t.Remove(host.Name)

			// if no dedicated actor service instance for the particular actor type,
			// we must delete consistent hashing table to avoid the memory leak.
			if len(t.Hosts()) == 0 {
				delete(s.data.hashingTableMap, e)
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

	s.lock.Lock()
	defer s.lock.Unlock()

	if m, ok := s.data.Members[host.Name]; ok {
		// No need to update consistent hashing table if the same dapr host member exists
		if m.AppID == host.AppID && m.Name == host.Name && cmp.Equal(m.Entities, host.Entities) {
			m.UpdatedAt = host.UpdatedAt
			return false
		}

		// Remove hashing table because the existing member is invalid
		// and needs to be updated by new member info.
		s.removeHashingTables(m)
	}

	s.data.Members[host.Name] = &DaprHostMember{
		Name:      host.Name,
		AppID:     host.AppID,
		UpdatedAt: host.UpdatedAt,
		APILevel:  host.APILevel,
	}

	// Update hashing table only when host reports actor types
	s.data.Members[host.Name].Entities = make([]string, len(host.Entities))
	copy(s.data.Members[host.Name].Entities, host.Entities)

	s.updateHashingTables(s.data.Members[host.Name])
	s.updateAPILevel()

	// Increase hashing table generation version. Runtime will compare the table generation
	// version with its own and then update it if it is new.
	s.data.TableGeneration++

	return true
}

// removeMember removes members from membership and update hashing table and returns true
// if hashing table update happens.
func (s *DaprHostMemberState) removeMember(host *DaprHostMember) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	if m, ok := s.data.Members[host.Name]; ok {
		s.removeHashingTables(m)
		s.data.TableGeneration++
		delete(s.data.Members, host.Name)
		s.updateAPILevel()

		return true
	}

	return false
}

func (s *DaprHostMemberState) isActorHost(host *DaprHostMember) bool {
	return len(host.Entities) > 0
}

// caller should hold lock.
func (s *DaprHostMemberState) restoreHashingTables() {
	if s.data.hashingTableMap == nil {
		s.data.hashingTableMap = map[string]*hashing.Consistent{}
	}

	for _, m := range s.data.Members {
		s.updateHashingTables(m)
	}
}

func (s *DaprHostMemberState) restore(r io.Reader) error {
	dec := codec.NewDecoder(r, &codec.MsgpackHandle{})
	var data DaprHostMemberStateData
	if err := dec.Decode(&data); err != nil {
		return err
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	s.data = data

	s.restoreHashingTables()
	s.updateAPILevel()
	return nil
}

func (s *DaprHostMemberState) persist(w io.Writer) error {
	s.lock.RLock()
	defer s.lock.RUnlock()

	b, err := marshalMsgPack(s.data)
	if err != nil {
		return err
	}

	if _, err := w.Write(b); err != nil {
		return err
	}

	return nil
}
