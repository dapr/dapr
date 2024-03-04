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
	"errors"
	"io"
	"strconv"
	"sync"

	"github.com/hashicorp/raft"

	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// CommandType is the type of raft command in log entry.
type CommandType uint8

const (
	// MemberUpsert is the command to update or insert new or existing member info.
	MemberUpsert CommandType = 0
	// MemberRemove is the command to remove member from actor host member state.
	MemberRemove CommandType = 1

	// TableDisseminate is the reserved command for dissemination loop.
	TableDisseminate CommandType = 100
)

// FSM implements a finite state machine that is used
// along with Raft to provide strong consistency. We implement
// this outside the Server to avoid exposing this outside the package.
type FSM struct {
	// stateLock is only used to protect outside callers to State() from
	// racing with Restore(), which is called by Raft (it puts in a totally
	// new state store). Everything internal here is synchronized by the
	// Raft side, so doesn't need to lock this.
	stateLock sync.RWMutex
	state     *DaprHostMemberState
	config    DaprHostMemberStateConfig
}

func newFSM(config DaprHostMemberStateConfig) *FSM {
	return &FSM{
		state:  newDaprHostMemberState(config),
		config: config,
	}
}

// State is used to return a handle to the current state.
func (c *FSM) State() *DaprHostMemberState {
	c.stateLock.RLock()
	defer c.stateLock.RUnlock()
	return c.state
}

// PlacementState returns the current placement tables.
// the withVirtualNodes parameter is here for backwards compatibility and should be removed in 1.14
// TODO in v1.15 remove the withVirtualNodes parameter
func (c *FSM) PlacementState() *v1pb.PlacementTables {
	c.stateLock.RLock()
	defer c.stateLock.RUnlock()
	withVirtualNodes := c.state.APILevel() < NoVirtualNodesInPlacementTablesAPILevel

	newTable := &v1pb.PlacementTables{
		Version:           strconv.FormatUint(c.state.TableGeneration(), 10),
		Entries:           make(map[string]*v1pb.PlacementTable),
		ApiLevel:          c.state.APILevel(),
		ReplicationFactor: c.config.replicationFactor,
	}

	totalHostSize := 0
	totalSortedSet := 0
	totalLoadMap := 0

	entries := c.state.hashingTableMap()
	for k, v := range entries {
		var table v1pb.PlacementTable
		v.ReadInternals(func(hosts map[uint64]string, sortedSet []uint64, loadMap map[string]*hashing.Host, totalLoad int64) {
			sortedSetLen := 0
			if withVirtualNodes {
				sortedSetLen = len(sortedSet)
			}

			table = v1pb.PlacementTable{
				Hosts:     make(map[uint64]string),
				SortedSet: make([]uint64, sortedSetLen),
				TotalLoad: totalLoad,
				LoadMap:   make(map[string]*v1pb.Host),
			}

			if withVirtualNodes {
				for lk, lv := range hosts {
					table.GetHosts()[lk] = lv
				}

				copy(table.GetSortedSet(), sortedSet)
			}

			for lk, lv := range loadMap {
				h := v1pb.Host{
					Name: lv.Name,
					Load: lv.Load,
					Port: lv.Port,
					Id:   lv.AppID,
				}
				table.LoadMap[lk] = &h
			}
		})

		newTable.Entries[k] = &table

		if withVirtualNodes {
			totalHostSize += len(table.GetHosts())
			totalSortedSet += len(table.GetSortedSet())
		}
		totalLoadMap += len(table.GetLoadMap())
	}

	if withVirtualNodes {
		logging.Debugf("PlacementTable Size, Hosts: %d, SortedSet: %d, LoadMap: %d", totalHostSize, totalSortedSet, totalLoadMap)
	} else {
		logging.Debugf("PlacementTable LoadMapCount=%d ApiLevel=%d ReplicationFactor=%d", totalLoadMap, newTable.GetApiLevel(), newTable.GetReplicationFactor())
	}

	return newTable
}

func (c *FSM) upsertMember(cmdData []byte) (bool, error) {
	var host DaprHostMember
	if err := unmarshalMsgPack(cmdData, &host); err != nil {
		return false, err
	}

	c.stateLock.RLock()
	defer c.stateLock.RUnlock()

	return c.state.upsertMember(&host), nil
}

func (c *FSM) removeMember(cmdData []byte) (bool, error) {
	var host DaprHostMember
	if err := unmarshalMsgPack(cmdData, &host); err != nil {
		return false, err
	}

	c.stateLock.RLock()
	defer c.stateLock.RUnlock()

	return c.state.removeMember(&host), nil
}

// Apply log is invoked once a log entry is committed.
func (c *FSM) Apply(log *raft.Log) interface{} {
	var (
		err     error
		updated bool
	)

	if log.Index < c.state.Index() {
		logging.Warnf("old: %d, new index: %d. skip apply", c.state.Index, log.Index)
		return false
	}

	if len(log.Data) < 2 {
		logging.Warnf("too short log data in raft logs: %v", log.Data)
		return false
	}

	switch CommandType(log.Data[0]) {
	case MemberUpsert:
		updated, err = c.upsertMember(log.Data[1:])
	case MemberRemove:
		updated, err = c.removeMember(log.Data[1:])
	default:
		err = errors.New("unimplemented command")
	}

	if err != nil {
		logging.Errorf("fsm apply entry log failed. data: %s, error: %s",
			string(log.Data), err.Error())
		return false
	}

	return updated
}

// Snapshot is used to support log compaction. This call should
// return an FSMSnapshot which can be used to save a point-in-time
// snapshot of the FSM.
func (c *FSM) Snapshot() (raft.FSMSnapshot, error) {
	return &snapshot{
		state: c.state.clone(),
	}, nil
}

// Restore streams in the snapshot and replaces the current state store with a
// new one based on the snapshot if all goes OK during the restore.
func (c *FSM) Restore(old io.ReadCloser) error {
	defer old.Close()

	members := newDaprHostMemberState(c.config)
	if err := members.restore(old); err != nil {
		return err
	}

	c.stateLock.Lock()
	c.state = members
	c.stateLock.Unlock()

	return nil
}
