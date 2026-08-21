/*
Copyright 2026 The Dapr Authors
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

// Package fsm carries the placement raft cluster's single replicated fact -
// whether this placement service has stood down in favor of scheduler
// placement. A leader elected after a failover then inherits the refusal
// from the log instead of racing its own scheduler watcher.
package fsm

import (
	"bytes"
	"io"
	"sync/atomic"

	"github.com/hashicorp/raft"
)

// StandDownCommand is the raft log entry committed by the leader once it has
// drained every placement stream.
var StandDownCommand = []byte("stand-down")

// ServeCommand is the raft log entry committed by the leader when the
// schedulers stopped serving placement after a stand-down.
var ServeCommand = []byte("serve")

type FSM struct {
	stoodDown atomic.Bool
}

func New() *FSM {
	return new(FSM)
}

// StoodDown reports whether a stand-down entry has been applied.
func (f *FSM) StoodDown() bool {
	return f.stoodDown.Load()
}

func (f *FSM) Apply(log *raft.Log) any {
	switch {
	case bytes.Equal(log.Data, StandDownCommand):
		f.stoodDown.Store(true)
	case bytes.Equal(log.Data, ServeCommand):
		f.stoodDown.Store(false)
	}
	return true
}

func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	return &snapshot{stoodDown: f.stoodDown.Load()}, nil
}

func (f *FSM) Restore(old io.ReadCloser) error {
	defer old.Close()
	// The snapshot is authoritative: state not in it must not survive.
	f.stoodDown.Store(false)
	data, err := io.ReadAll(old)
	if err != nil {
		return err
	}
	if bytes.Equal(data, StandDownCommand) {
		f.stoodDown.Store(true)
	}
	return nil
}

type snapshot struct {
	stoodDown bool
}

func (s *snapshot) Persist(sink raft.SnapshotSink) error {
	if s.stoodDown {
		if _, err := sink.Write(StandDownCommand); err != nil {
			//nolint:errcheck
			sink.Cancel()
			return err
		}
	}
	return sink.Close()
}

func (s *snapshot) Release() {}
