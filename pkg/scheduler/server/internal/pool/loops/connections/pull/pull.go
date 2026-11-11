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

package pull

import (
	"time"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/serialize"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.connections.pull")

// defaultReattachGrace is how long a pull trigger whose stream closed while it
// ran waits for its sidecar to reconnect and report it as still running. A
// sidecar that does keeps the activity, so a live activity is not run twice;
// the activity of a sidecar that does not is delivered again after this long.
const defaultReattachGrace = 30 * time.Second

// Options wires a Dispatcher to the connections loop of its namespace.
type Options struct {
	// Loop is the connections loop. Every Dispatcher method runs on it, and
	// the hooks the Dispatcher installs on its triggers queue their work to it.
	Loop loop.Interface[loops.EventConn]

	// StreamLoop returns the loop of a connected stream.
	StreamLoop func(streamIDx uint64) (loop.Interface[loops.EventStream], bool)

	// AcquireGates acquires all of a trigger's shared concurrency gates or
	// none, returning the keys held, or the key of the gate that was full.
	AcquireGates func(req *loops.TriggerRequest) (keys []string, full string, ok bool)

	// ReleaseGates releases gates acquired for a trigger.
	ReleaseGates func(keys []string)

	// Requeue routes a trigger again from the start.
	Requeue func(req *loops.TriggerRequest)

	// Idle is called when the last unresolved pull trigger has resolved.
	Idle func()

	// MaxParked bounds the parked pull triggers per actor type. A trigger
	// beyond it is resolved FAILED, so its failure policy retries it later.
	MaxParked int
}

// Dispatcher holds the pull dispatch state of one namespace's connections
// loop. pools lists, per actor type, the sidecars reporting pull slots; a
// sidecar's several streams share one pool. pending parks pull triggers per
// actor type while no pool has a free slot. sidecars holds each sidecar's
// stream count and the pull triggers dispatched to it, which outlive the
// stream that delivered them. rotate offsets the slot search so equally
// loaded pools take turns.
type Dispatcher struct {
	opts  Options
	grace time.Duration

	streams  map[uint64]string
	pools    map[string][]*pool
	pending  map[string]*queue
	claims   map[string]*claim
	sidecars map[string]*sidecar
	backlog  *gauge
	inflight *gauge
	rotate   int
}

func New(opts Options) *Dispatcher {
	return &Dispatcher{
		opts:     opts,
		grace:    defaultReattachGrace,
		streams:  make(map[uint64]string),
		pools:    make(map[string][]*pool),
		pending:  make(map[string]*queue),
		claims:   make(map[string]*claim),
		sidecars: make(map[string]*sidecar),
		backlog:  newGauge(monitoring.RecordWorkflowActivityBacklog),
		inflight: newGauge(monitoring.RecordWorkflowActivityInflight),
	}
}

// refKey is the stored job key of the reminder ref names, the key the cron
// sets on its triggers.
func refKey(namespace string, ref *schedulerv1pb.ActorReminderRef) string {
	return serialize.ActorReminderKey(namespace, ref.GetActorType(), ref.GetActorId(), ref.GetName())
}

// SetRotation seeds the slot search, so equally loaded sidecars are not all
// picked first by every scheduler at once.
func (d *Dispatcher) SetRotation(schedulerIdx int) {
	d.rotate = schedulerIdx
}

// Shutdown resolves every parked and dispatched pull trigger UNDELIVERABLE.
// Called after every stream is closed.
func (d *Dispatcher) Shutdown() {
	for _, q := range d.pending {
		q.drainAll(func(req *loops.TriggerRequest) {
			d.backlog.add(req, -1)
			req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		})
	}
	for _, sc := range d.sidecars {
		for _, f := range sc.flights {
			f.stopTimer()
			f.req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			// The loop is closing, so the flight's own end never runs.
			d.inflight.add(f.req, -1)
		}
	}
	for _, c := range d.claims {
		if c.timer != nil {
			c.timer.Stop()
		}
		if c.req != nil {
			c.req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		}
	}
}
