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
	"sync/atomic"
	"time"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// claim is a pull reminder a sidecar listed as running that this scheduler
// has no flight for, such as after this scheduler restarted. When the cron
// fires it again it goes back to that sidecar, or is resolved with the
// result the sidecar already sent. A claim exists only once its sidecar has
// reconnected: a reminder fired before then can go to another sidecar,
// within the at-least-once guarantee.
// ponytail: a claim for a reminder that never fires again (its workflow was
// purged) lasts until its sidecar disconnects; bounded by the activities in
// flight at a restart.
type claim struct {
	sidecar string
	// status is the result the sidecar sent before the cron fired the
	// reminder again, kept until timer: a reminder that never fires again
	// ran elsewhere or was deleted, and a later one of the same name must
	// still run.
	status *schedulerv1pb.WatchJobsRequestResultStatus
	timer  *time.Timer
	// req is the cron's trigger, held until the sidecar can take it.
	req *loops.TriggerRequest
}

// flight is a pull trigger dispatched to a sidecar and not yet resolved. done
// is set once by whichever of the result, repark or expiry gets there first;
// the hooks run on stream goroutines, everything else on the loop.
type flight struct {
	sidecar   string
	actorType string
	key       string
	req       *loops.TriggerRequest
	gateKeys  []string
	done      atomic.Bool

	// orphaned: the stream that delivered it closed. confirmed: a later
	// stream of the sidecar listed it as still running. An orphaned flight
	// that is not confirmed is resolved UNDELIVERABLE when timer fires.
	orphaned  bool
	confirmed bool
	timer     *time.Timer
}

// repark queues a trigger the sidecar refused for lack of a slot again,
// without resolving it to the cron. The sidecar was full, so its credit is
// zero until it reports otherwise.
func (d *Dispatcher) repark(f *flight, cronFn func(api.TriggerResponseResult), gateKeys []string) {
	d.endFlight(f)
	if p := d.poolOf(f.actorType, f.sidecar); p != nil {
		p.credit = 0
	}
	d.opts.ReleaseGates(gateKeys)
	meta := f.req.Job.GetMetadata()
	monitoring.RecordWorkflowActivityRefused(meta.GetNamespace(), meta.GetAppId(), meta.GetConcurrencyKey())
	// A fresh request: a closed stream may still hold the refused one, whose
	// hooks are now no-ops.
	req := &loops.TriggerRequest{Job: f.req.Job, ResultFn: cronFn}
	if len(d.pools[f.actorType]) == 0 {
		d.opts.Requeue(req)
		return
	}
	if dispatched, throttledKey := d.tryDispatch(req); !dispatched {
		d.park(req, throttledKey, true)
	}
}

// orphan marks a flight whose stream closed. It is delivered again after the
// reattach window unless a stream of the sidecar reports it as running.
func (d *Dispatcher) orphan(f *flight) {
	if f.done.Load() || !d.isCurrent(f) {
		return
	}
	f.orphaned = true
	if sc := d.sidecars[f.sidecar]; !f.confirmed || sc.streams == 0 {
		d.startTimer(f)
	}
}

// startTimer starts f's reattach window, which a later listing ends.
func (d *Dispatcher) startTimer(f *flight) {
	f.confirmed = false
	if f.timer != nil {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(d.grace, func() {
		d.opts.Loop.Enqueue(&loops.ConnRun{Fn: func() {
			if f.timer != t || f.done.Load() {
				return
			}
			f.timer = nil
			log.Debugf("Pull trigger %s was not reattached by sidecar %s; delivering it again", f.key, f.sidecar)
			f.req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		}})
	})
	f.timer = t
}

func (f *flight) stopTimer() {
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
}

func (d *Dispatcher) isCurrent(f *flight) bool {
	sc, ok := d.sidecars[f.sidecar]
	return ok && sc.flights[f.key] == f
}

// endFlight forgets a resolved or reparked flight.
func (d *Dispatcher) endFlight(f *flight) {
	f.stopTimer()
	if !d.isCurrent(f) {
		return
	}
	delete(d.sidecars[f.sidecar].flights, f.key)
	d.inflight.add(f.req, -1)
	d.maybeDropSidecar(f.sidecar)
	d.opts.Idle()
}

// HasFlights reports whether a pull trigger dispatched by this loop is still
// unresolved, or a sidecar's result waits for the cron to fire its reminder
// again. The loop is kept while either holds, even with no stream connected,
// so the sidecar can reattach and the result is not lost.
func (d *Dispatcher) HasFlights() bool {
	for _, sc := range d.sidecars {
		if len(sc.flights) > 0 {
			return true
		}
	}
	for _, c := range d.claims {
		if c.status != nil {
			return true
		}
	}
	return false
}

// MarkGates adds the gates held by unresolved pull triggers to referenced:
// they outlive the stream that delivered them, and dropping their gates
// would let a reconnect start counting again from zero. It reports whether
// a gate is referenced only by those triggers.
func (d *Dispatcher) MarkGates(referenced map[string]struct{}) (retained bool) {
	for _, sc := range d.sidecars {
		for _, f := range sc.flights {
			for _, key := range f.gateKeys {
				if _, ok := referenced[key]; !ok {
					referenced[key] = struct{}{}
					retained = true
				}
			}
		}
	}
	return retained
}
