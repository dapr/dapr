// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"io"
	"sync"

	"github.com/hashicorp/go-msgpack/codec"
	"github.com/hashicorp/raft"
)

// CommandType is the type of raft command in log entry
type CommandType uint8

const (
	// MemberUpsert is the command to update or insert new or existing member info
	MemberUpsert CommandType = 0
	// MemberRemove is the command to remove member from actor host member state
	MemberRemove = 1

	// MemberFlush is the reserved command for dessemination loop
	MemberFlush = 100
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

// State is used to return a handle to the current state
func (c *FSM) State() *DaprHostMemberState {
	c.stateLock.RLock()
	defer c.stateLock.RUnlock()
	return c.state
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
	buf := log.Data
	cmdType := CommandType(buf[0])

	if log.Index < c.state.Index {
		logging.Warnf("old: %d, new index: %d. skip apply", c.state.Index, log.Index)
		return nil
	}

	var err error
	var updated bool
	switch cmdType {
	case MemberUpsert:
		updated, err = c.upsertMember(buf[1:])
	case MemberRemove:
		updated, err = c.removeMember(buf[1:])
	}

	if err != nil {
		return err
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
