// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
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
	if err := s.state.persist(sink); err != nil {
		sink.Cancel()
	}

	return sink.Close()
}

// Release releases the state storage resource. No-Ops because we use the
// in-memory state.
func (s *snapshot) Release() {}
