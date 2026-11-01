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
	"net"
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/utils"
)

// OrderOp is the protocol-neutral placement order operation.
type OrderOp int

const (
	OrderLock OrderOp = iota
	OrderUpdate
	OrderUnlock
)

func (o OrderOp) String() string {
	switch o {
	case OrderLock:
		return "lock"
	case OrderUpdate:
		return "update"
	case OrderUnlock:
		return "unlock"
	default:
		return "unknown"
	}
}

// Order is the protocol-neutral placement order produced by a stream
// transport. Exactly one of V1Tables and V2Tables is set on UPDATE,
// depending on which protocol the stream speaks.
type Order struct {
	Op OrderOp

	// Version is the dissemination round this order belongs to: the global
	// table version on the v1 (placement service) protocol, the round seq on
	// the v2 (scheduler) protocol. Echoed back in the matching Ack.
	Version uint64

	// Scope is the actor types a LOCK or UNLOCK applies to. Empty means all
	// actor types. Always empty on the v1 protocol.
	Scope []string

	// Versions are the per actor type table versions of an UPDATE. Only set
	// on the v2 protocol.
	Versions map[string]uint64

	// V1Tables is the full-table UPDATE payload of the v1 protocol.
	V1Tables *v1pb.PlacementTables

	// V2Tables is the partial-table UPDATE payload of the v2 protocol.
	V2Tables *schedulerv1pb.PlacementTables

	// Partial is true when the UPDATE payload merges into the existing
	// tables (v2) rather than replacing them wholesale (v1).
	Partial bool
}

// Report is the protocol-neutral presence and actor types report sent to the
// placement server.
type Report struct {
	// Address is the daprd internal gRPC host:port.
	Address    string
	AppID      string
	Namespace  string
	ActorTypes []string
}

// Clone returns a deep copy of the report.
func (r *Report) Clone() *Report {
	cp := *r
	cp.ActorTypes = append([]string(nil), r.ActorTypes...)
	return &cp
}

// Ack is the protocol-neutral acknowledgement of a placement order phase.
type Ack struct {
	Op OrderOp

	// Version echoes Order.Version of the order being acked.
	Version uint64
}

type placebase struct{}

func (*placebase) isEventPlace() {}

type EventPlace interface{ isEventPlace() }

type dissbase struct{}

func (*dissbase) isEventDiss() {}

type EventDiss interface{ isEventDiss() }

type streambase struct{}

func (*streambase) isEventStream() {}

type EventStream interface{ isEventStream() }

type lookupbase struct{}

func (*lookupbase) isEventLookup() {}

type EventLookup interface{ isEventLookup() }

type PlacementReconnect struct {
	*placebase
	ActorTypes *[]string
	// TransientPrior is true when this reconnect was triggered by a close
	// that was itself a transient "not a leader" rejection (i.e. routine
	// placement leadership churn). Consumers use it to demote per-cycle
	// log lines on the connect path to debug so the rapid back-to-back
	// reconnects under leader churn don't spam the runtime log. Initial
	// startup and real failures leave this as false.
	TransientPrior bool
}

type UpdateTypes struct {
	*placebase
	ActorTypes []string
}

// ReloadReminderTypes reloads the actor types advertised to the scheduler for
// reminder delivery, after the actor table changed them in place.
type ReloadReminderTypes struct {
	*placebase
	*dissbase
}

// WatchRemoteHosts asks the placement loop for a channel which receives
// whether a daprd other than this one hosts ActorType. The loop creates the
// channel and replies with it on Resp, which must have room for the reply.
// The loop is the only sender on the channel and its only closer: it closes
// it once Ctx is done, or at shutdown.
type WatchRemoteHosts struct {
	*placebase
	ActorType string
	Ctx       context.Context
	Resp      chan<- chan bool
}

// UnwatchRemoteHosts ends the WatchRemoteHosts subscription of Ch and closes
// Ch. The placement loop enqueues it itself once the Ctx of the watch is done.
type UnwatchRemoteHosts struct {
	*placebase
	ActorType string
	Ch        chan bool
}

// RoundCompleted tells the placement loop what the installed tables show
// once a dissemination round completed.
type RoundCompleted struct {
	*placebase

	// Remote holds the actor types a daprd other than this one hosts. A type
	// missing from it has no remote host, unless it is pending.
	Remote map[string]bool

	// Pending are the actor types a round still in progress covers: their
	// installed tables are not final, so Remote says nothing about them.
	// PendingAll is set when such a round covers every type.
	Pending    map[string]struct{}
	PendingAll bool
}

type ReportHost struct {
	*dissbase
	Report *Report
}

type StreamOrder struct {
	*placebase
	*dissbase
	Order *Order
	IDx   uint64
}

// StreamSend sends a message on the placement stream. Exactly one of Report
// and Ack is set.
type StreamSend struct {
	*streambase
	Report *Report
	Ack    *Ack
}

type LookupRequest struct {
	*placebase
	*dissbase
	*lookupbase
	Request  *api.LookupActorRequest
	Context  context.Context
	Response chan<- *LookupResponse
}

type LookupResponse struct {
	Response *api.LookupActorResponse
	Context  context.Context
	Cancel   context.CancelCauseFunc
	Error    error
}

type LockRequest struct {
	*placebase
	*dissbase
	*lookupbase
	ActorType string
	Context   context.Context
	Response  chan<- *LockResponse
}

type LockResponse struct {
	Context context.Context
	Cancel  context.CancelCauseFunc
}

type ConnCloseStream struct {
	*placebase
	Error error
	IDx   uint64
}

type Shutdown struct {
	*placebase
	*dissbase
	*streambase
	Error error
}

type DisseminationTimeout struct {
	*dissbase
	Version uint64
}

type SetDrainOngoingCallTimeout struct {
	*placebase
	Drain   *bool
	Timeout *time.Duration
}

// SetEntityDrainOngoingCallTimeouts replaces the per-actor-type drain
// timeouts. nil/empty means "remove all overrides".
type SetEntityDrainOngoingCallTimeouts struct {
	*placebase
	Timeouts map[string]time.Duration
}

func IsActorLocal(targetActorAddress, hostAddress string, port string) bool {
	if targetActorAddress == net.JoinHostPort(hostAddress, port) {
		// Easy case when there is a perfect match
		return true
	}

	if utils.IsLocalhost(hostAddress) {
		tHost, tPort, err := net.SplitHostPort(targetActorAddress)
		if err == nil && tPort == port {
			return utils.IsLocalhost(tHost)
		}
	}

	return false
}
