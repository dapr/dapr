// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"io"
	"strconv"
	"sync"

	"github.com/hashicorp/go-msgpack/codec"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"

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
}

func newFSM() *FSM {
	return &FSM{
		state: newDaprHostMemberState(),
	}
}

// State is used to return a handle to the current state.
func (c *FSM) State() *DaprHostMemberState {
	c.stateLock.RLock()
	defer c.stateLock.RUnlock()
	return c.state
}

// PlacementState returns the current placement tables.
func (c *FSM) PlacementState() *v1pb.PlacementTables {
	c.stateLock.RLock()
	defer c.stateLock.RUnlock()

	newTable := &v1pb.PlacementTables{
		Version: strconv.FormatUint(c.state.TableGeneration, 10),
		Entries: make(map[string]*v1pb.PlacementTable),
	}

	totalHostSize := 0
	totalSortedSet := 0
	totalLoadMap := 0

	entries := c.state.hashingTableMap
	for k, v := range entries {
		hosts, sortedSet, loadMap, totalLoad := v.GetInternals()
		table := v1pb.PlacementTable{
			Hosts:     make(map[uint64]string),
			SortedSet: make([]uint64, len(sortedSet)),
			TotalLoad: totalLoad,
			LoadMap:   make(map[string]*v1pb.Host),
		}

		for lk, lv := range hosts {
			table.Hosts[lk] = lv
		}

		copy(table.SortedSet, sortedSet)

		for lk, lv := range loadMap {
			h := v1pb.Host{
				Name: lv.Name,
				Load: lv.Load,
				Port: lv.Port,
				Id:   lv.AppID,
			}
			table.LoadMap[lk] = &h
		}
		newTable.Entries[k] = &table

		totalHostSize += len(table.Hosts)
		totalSortedSet += len(table.SortedSet)
		totalLoadMap += len(table.LoadMap)
	}

	logging.Debugf("PlacementTable Size, Hosts: %d, SortedSet: %d, LoadMap: %d", totalHostSize, totalSortedSet, totalLoadMap)

	return newTable
}

func (c *FSM) upsertMember(cmdData []byte) (bool, error) {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()

	var host DaprHostMember
	if err := unmarshalMsgPack(cmdData, &host); err != nil {
		return false, err
	}

	return c.state.upsertMember(&host), nil
}

func (c *FSM) removeMember(cmdData []byte) (bool, error) {
	c.stateLock.Lock()
	defer c.stateLock.Unlock()

	var host DaprHostMember
	if err := unmarshalMsgPack(cmdData, &host); err != nil {
		return false, err
	}

	return c.state.removeMember(&host), nil
}

// Apply log is invoked once a log entry is committed.
func (c *FSM) Apply(log *raft.Log) interface{} {
	var (
		err     error
		updated bool
	)

	if log.Index < c.state.Index {
		logging.Warnf("old: %d, new index: %d. skip apply", c.state.Index, log.Index)
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

	dec := codec.NewDecoder(old, &codec.MsgpackHandle{})
	var members DaprHostMemberState
	if err := dec.Decode(&members); err != nil {
		return err
	}

	c.stateLock.Lock()
	c.state = &members
	c.state.restoreHashingTables()
	c.stateLock.Unlock()

	return nil
}
