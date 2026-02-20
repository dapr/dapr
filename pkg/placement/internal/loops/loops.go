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

type nsbase struct{}

func (*nsbase) isEventNamespace() {}

type EventNamespace interface{ isEventNamespace() }

type dissbase struct{}

func (*dissbase) isEventDisseminator() {}

type EventDisseminator interface{ isEventDisseminator() }

type streambase struct{}

func (*streambase) isEventStream() {}

type EventStream interface{ isEventStream() }

// ConnAdd is the event for adding a new connection to placement.
type ConnAdd struct {
	*nsbase
	*dissbase
	Channel     v1pb.Placement_ReportDaprStatusServer
	Cancel      context.CancelCauseFunc
	InitialHost *v1pb.Host
}

type ReportedHost struct {
	*nsbase
	*dissbase
	Host      *v1pb.Host
	StreamIDx uint64
}

type DisseminateLock struct {
	*streambase
	Version uint64
}

type DisseminateUpdate struct {
	*streambase
	Tables  *v1pb.PlacementTables
	Version uint64
}

type DisseminateUnlock struct {
	*streambase
	Version uint64
}

// StreamShutdown is the event for shutting down the placement client stream.
type StreamShutdown struct {
	*nsbase
	*streambase
	Error error
}

// ConnCloseStream is the event for closing a connection to the placement from
// the stream.
type ConnCloseStream struct {
	*nsbase
	*dissbase
	StreamIDx uint64
	Namespace string
	Error     error
}

// Shutdown is the event for shutting down the placement loops.
type Shutdown struct {
	*nsbase
	*dissbase
	Error error
}

type DisseminationTimeout struct {
	*dissbase
	Version uint64
}

type NamespaceTableRequest struct {
	*dissbase
	Table func(*v1pb.StatePlacementTable)
}

type StateTableRequest struct {
	*nsbase
	State func(*v1pb.StatePlacementTables)
}
