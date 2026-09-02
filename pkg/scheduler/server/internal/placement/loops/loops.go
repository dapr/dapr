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

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type nsbase struct{}

func (*nsbase) isEventNamespace() {}

// EventNamespace is an event for the top level namespaces loop.
type EventNamespace interface{ isEventNamespace() }

type connsbase struct{}

func (*connsbase) isEventConnections() {}

// EventConnections is an event for a per namespace connections loop.
type EventConnections interface{ isEventConnections() }

type streambase struct{}

func (*streambase) isEventStream() {}

// EventStream is an event for a per stream loop.
type EventStream interface{ isEventStream() }

// ConnAdd adds a new placement stream.
type ConnAdd struct {
	*nsbase
	*connsbase
	Channel schedulerv1pb.Scheduler_ReportActorTypesServer
	Cancel  context.CancelCauseFunc
	Initial *schedulerv1pb.ActorHost
}

// SetLeader marks this scheduler as placement leader or not. Losing
// leadership closes every stream and clears all state.
type SetLeader struct {
	*nsbase
	Leader bool
}

// ReportedTypes is a (re-)report of a host's actor types from a stream.
type ReportedTypes struct {
	*nsbase
	*connsbase
	StreamIDx uint64
	Host      *schedulerv1pb.ActorHost
}

// Ack acknowledges a placement order phase from a stream.
type Ack struct {
	*nsbase
	*connsbase
	StreamIDx uint64
	Namespace string
	Operation schedulerv1pb.Operation
	Seq       uint64
}

// ConnCloseStream closes a stream, removing it from its namespace.
type ConnCloseStream struct {
	*nsbase
	*connsbase
	StreamIDx uint64
	Namespace string
	Error     error
}

// ConnCloseNamespace is sent by a namespace's connections loop when its last
// stream is removed, confirming the namespace may be torn down.
type ConnCloseNamespace struct {
	*nsbase
	Namespace string
}

// Shutdown shuts down a loop and everything below it.
type Shutdown struct {
	*nsbase
	*connsbase
	Error error
}

// RoundTimeout fires when a dissemination round exceeds the disseminate
// timeout.
type RoundTimeout struct {
	*connsbase
	Seq uint64
}

// CoalesceFire drains the pending type changes accumulated during the
// coalesce window into a dissemination round.
type CoalesceFire struct {
	*connsbase
}

// SendLock sends a LOCK order to a stream.
type SendLock struct {
	*streambase
	Seq   uint64
	Types []string
}

// SendUpdate sends an UPDATE order to a stream.
type SendUpdate struct {
	*streambase
	Seq      uint64
	Types    []string
	Versions map[string]uint64
	Tables   *schedulerv1pb.PlacementTables
}

// SendUnlock sends an UNLOCK order to a stream.
type SendUnlock struct {
	*streambase
	Seq   uint64
	Types []string
}

// SendSnapshot sends a one-shot full LOCK+UPDATE+UNLOCK snapshot of all
// placement tables to a stream, outside of any cluster wide round.
type SendSnapshot struct {
	*streambase
	Seq      uint64
	Versions map[string]uint64
	Tables   *schedulerv1pb.PlacementTables
}

// StreamShutdown closes a single stream loop.
type StreamShutdown struct {
	*streambase
	Error error
}
