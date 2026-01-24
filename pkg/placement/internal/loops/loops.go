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

package loops

import (
	"context"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

// Event is a generic interface for all events in placement.
type Event any

// ConnAdd is the event for adding a new connection to placement.
type ConnAdd struct {
	Channel     v1pb.Placement_ReportDaprStatusServer
	Cancel      context.CancelCauseFunc
	InitialHost *v1pb.Host
}

type ReportedHost struct {
	Host      *v1pb.Host
	StreamIDx uint64
}

type DisseminateLock struct {
	Version uint64
}

type DisseminateUpdate struct {
	Tables  *v1pb.PlacementTables
	Version uint64
}

type DisseminateUnlock struct {
	Version uint64
}

// StreamShutdown is the event for shutting down the placement client stream.
type StreamShutdown struct {
	Error error
}

// ConnCloseStream is the event for closing a connection to the placement from
// the stream.
type ConnCloseStream struct {
	StreamIDx uint64
	Namespace string
	Error     error
}

// Shutdown is the event for shutting down the placement loops.
type Shutdown struct{}

type DisseminationTimeout struct {
	Version uint64
}

type NamespaceTableRequest struct {
	Table func(*v1pb.StatePlacementTable)
}

type StateTableRequest struct {
	State func(*v1pb.StatePlacementTables)
}
