/*
Copyright 2025 The Dapr Authors
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
	"strconv"

	"github.com/diagridio/go-etcd-cron/api"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type nsbase struct{}

func (*nsbase) isEventNS() {}

type EventNS interface{ isEventNS() }

type connbase struct{}

func (*connbase) isEventConn() {}

type EventConn interface{ isEventConn() }

type streambase struct{}

func (*streambase) isEventStream() {}

type EventStream interface{ isEventStream() }

// TriggerRequest is the event for triggering a job in the scheduler.
type TriggerRequest struct {
	*nsbase
	*connbase
	*streambase
	Job      *internalsv1pb.JobEvent
	ResultFn func(api.TriggerResponseResult)

	// Repark and Orphan are set on a pull-dispatched trigger. The stream calls
	// Repark instead of ResultFn when the sidecar answers NO_CAPACITY, and
	// Orphan instead of resolving the trigger UNDELIVERABLE when the stream
	// closes with it in flight, so the sidecar can reattach to it.
	Repark func()
	Orphan func()
}

// StreamShutdown is the event for shutting down the scheduler stream.
type StreamShutdown struct {
	*streambase
}

// ConnAdd is the event for adding a new connection to the scheduler.
type ConnAdd struct {
	*nsbase
	*connbase
	Channel schedulerv1pb.Scheduler_WatchJobsServer
	Request *schedulerv1pb.WatchJobsRequestInitial
	Cancel  context.CancelCauseFunc
}

// ConnCloseStream is the event for closing a connection to the scheduler from
// the stream.
type ConnCloseStream struct {
	*nsbase
	*connbase
	StreamIDx uint64
	Namespace string
}

// ConnCloseNamespace is sent by a namespace's connections loop when its last
// stream has been removed. The connections loop owns the authoritative stream
// set, so namespace deletion is driven by this confirmation rather than by
// per-event counting alone, which a stray or duplicate close event could
// otherwise corrupt into deleting a namespace that still has live streams.
type ConnCloseNamespace struct {
	*nsbase
	Namespace string
}

type ConcurrencyRelease struct {
	*connbase
	GateKeys []string
}

// PullSlotsUpdate carries the free pull slots a sidecar reported on a stream.
type PullSlotsUpdate struct {
	*nsbase
	*connbase
	Namespace string
	Sidecar   string
	ActorType string
	Free      uint32
}

// PullResult carries the result of a pull-dispatched reminder that a sidecar
// identified by reference because the stream that delivered it closed.
type PullResult struct {
	*nsbase
	*connbase
	Namespace string
	Sidecar   string
	Reminder  *schedulerv1pb.ActorReminderRef
	Status    schedulerv1pb.WatchJobsRequestResultStatus
}

// ConnRun runs Fn on the connections loop. Used by the hooks the loop
// installs on the triggers it dispatches.
type ConnRun struct {
	*connbase
	Fn func()
}

// SidecarID identifies the sidecar behind a stream by its actor address, so
// its several streams to a scheduler share pull state. A stream without an
// actor address is its own sidecar.
func SidecarID(streamIDx uint64, actorAddress string) string {
	if actorAddress != "" {
		return actorAddress
	}
	return "idx:" + strconv.FormatUint(streamIDx, 10)
}

// SchedulerInfoUpdate propagates the current scheduler cluster size and this
// scheduler's index within it, pushed by the leadership loop whenever the host
// list changes. Flows pool -> namespaces -> every connection loop.
type SchedulerInfoUpdate struct {
	*nsbase
	*connbase
	Count int32
	Idx   int32
}

// Shutdown is the event for shutting down the scheduler loops.
type Shutdown struct {
	*nsbase
	*connbase
}
