// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package raft

import (
	"github.com/hashicorp/raft"
)

// snapshot is used to provide a snapshot of the current
// state in a way that can be accessed concurrently with operations
// that may modify the live state.
type snapshot struct {
	state *DaprHostMemberState
}

// Persist saves the FSM snapshot out to the given sink.
func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	b, err := marshalMsgPack(s.state)
	if err != nil {
		sink.Cancel()
		return err
	}

	if _, err := sink.Write(b); err != nil {
		sink.Cancel()
		return err
	}

	return sink.Close()
}

// Release releases the state storage resource. No-Ops because we use the
// in-memory state.
func (s *snapshot) Release() {}
