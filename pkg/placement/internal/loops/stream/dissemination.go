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

package stream

import (
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/ptr"
)

const (
	operationLock   = "lock"
	operationUnlock = "unlock"
	operationUpdate = "update"
)

// handleLock sends the lock operation to the stream.
func (s *stream) handleLock(version uint64) error {
	// Set the current version on lock operations as this is the latest known
	// version.
	if s.currentVersion != nil && *s.currentVersion > version {
		return nil
	}

	s.currentVersion = ptr.Of(version)

	log.Debugf("Sending LOCK for version %d to stream %s:%d", version, s.ns, s.idx)

	return s.channel.Send(&v1pb.PlacementOrder{
		Operation: operationLock,
		Version:   version,
	})
}

// handleUpdate sends the update operation to the stream with the new tables.
func (s *stream) handleUpdate(version uint64, tables *v1pb.PlacementTables) error {
	// Ignore outdated updates.
	if s.currentVersion == nil || *s.currentVersion != version {
		return nil
	}

	log.Debugf("Sending UPDATE for version %d to stream %s:%d", version, s.ns, s.idx)

	return s.channel.Send(&v1pb.PlacementOrder{
		Operation: operationUpdate,
		Version:   version,
		Tables:    tables,
	})
}

// handleUnlock sends the unlock operation to the stream.
func (s *stream) handleUnlock(version uint64) error {
	// Ignore unlocks for older versions.
	if s.currentVersion == nil || *s.currentVersion != version {
		return nil
	}

	log.Debugf("Sending UNLOCK for version %d to stream %s:%d", version, s.ns, s.idx)

	return s.channel.Send(&v1pb.PlacementOrder{
		Operation: operationUnlock,
		Version:   version,
	})
}
