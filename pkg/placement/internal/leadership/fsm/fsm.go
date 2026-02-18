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

package fsm

import (
	"io"

	"github.com/hashicorp/raft"
)

type noop struct{}

func New() raft.FSM {
	return new(noop)
}

func (*noop) Apply(log *raft.Log) any {
	return true
}

func (*noop) Snapshot() (raft.FSMSnapshot, error) {
	return new(snapshot), nil
}

// Restore streams in the snapshot and replaces the current state store with a
// new one based on the snapshot if all goes OK during the restore.
func (*noop) Restore(old io.ReadCloser) error {
	return nil
}

type snapshot struct{}

func (s *snapshot) Persist(raft.SnapshotSink) error {
	return nil
}

func (s *snapshot) Release() {}
