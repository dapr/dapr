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
	"slices"

	"github.com/diagridio/go-etcd-cron/api"

	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// HandleTrigger dispatches a pull-flagged actor reminder trigger to a sidecar
// with a free slot, or parks it until one frees. It returns false when pull
// dispatch does not apply (not a pull trigger, not an actor target, or no
// connected sidecar reports slots for the actor type), in which case the
// caller routes the trigger as usual.
func (d *Dispatcher) HandleTrigger(req *loops.TriggerRequest) bool {
	meta := req.Job.GetMetadata()
	if !meta.GetPull() {
		return false
	}
	actor := meta.GetTarget().GetActor()
	if actor == nil {
		return false
	}
	if len(d.pools[actor.GetType()]) == 0 {
		log.Debugf("Pull trigger %s has no eligible sidecar for its actor type; using regular routing", req.Job.GetName())
		return false
	}

	if dispatched, throttledKey := d.tryDispatch(req); !dispatched {
		d.park(req, throttledKey, false)
	}
	return true
}

// park queues req until a sidecar has a free slot. A new trigger beyond
// MaxParked is resolved FAILED, so its failure policy retries it later; an
// admitted one, which a sidecar refused, is parked regardless.
func (d *Dispatcher) park(req *loops.TriggerRequest, throttledKey string, admitted bool) {
	actor := req.Job.GetMetadata().GetTarget().GetActor()
	q, ok := d.pending[actor.GetType()]
	if !ok {
		q = newQueue(d.opts.MaxParked)
		d.pending[actor.GetType()] = q
	}
	if !q.enqueue(req, admitted) {
		req.ResultFn(api.TriggerResponseResult_FAILED)
		return
	}
	log.Debugf("Parked pull trigger %s for actor %s: no free slot on %d eligible sidecars (%d waiting)", req.Job.GetName(), actor.GetId(), len(d.pools[actor.GetType()]), q.len())
	// Waiting for a sidecar slot is the backlog gauge's job; only a shared
	// type or per-name gate counts as a concurrency throttle.
	if throttledKey != "" {
		monitoring.RecordConcurrencyThrottled(throttledKey)
	}
	d.backlog.add(req, 1)
}

// tryDispatch hands req to the eligible sidecar with the most free slots once
// the trigger's type and per-name gates are acquired. When it does not
// dispatch it leaves no gate held and returns the gate key that was full, or
// "" when no sidecar had a free slot.
func (d *Dispatcher) tryDispatch(req *loops.TriggerRequest) (dispatched bool, throttledKey string) {
	p, ok := d.pickPool(req.Job.GetMetadata().GetTarget().GetActor().GetType())
	if !ok {
		return false, ""
	}
	return d.dispatch(req, p)
}

// dispatch hands req to p's newest stream once its type and per-name gates
// are acquired, taking one of p's slots, and tracks it until resolved. When
// it does not dispatch it leaves no gate held and returns the gate key that
// was full.
func (d *Dispatcher) dispatch(req *loops.TriggerRequest, p *pool) (dispatched bool, throttledKey string) {
	// The sidecar's newest stream: an older one may be draining.
	streamIDx := p.streams[len(p.streams)-1]
	streamLoop, ok := d.opts.StreamLoop(streamIDx)
	if !ok {
		return false, ""
	}
	gateKeys, full, ok := d.opts.AcquireGates(req)
	if !ok {
		return false, full
	}
	p.credit--

	actor := req.Job.GetMetadata().GetTarget().GetActor()
	f := &flight{
		sidecar:   p.sidecar,
		actorType: actor.GetType(),
		key:       req.Job.GetKey(),
		req:       req,
		gateKeys:  gateKeys,
	}
	d.sidecarFor(p.sidecar).flights[f.key] = f
	d.inflight.add(req, 1)

	cronFn := req.ResultFn
	req.ResultFn = func(result api.TriggerResponseResult) {
		if !f.done.CompareAndSwap(false, true) {
			return
		}
		// Queued before the cron learns the result, so the flight has ended
		// before the cron can fire the reminder again.
		d.opts.Loop.Enqueue(&loops.ConnRun{Fn: func() {
			d.endFlight(f)
			d.opts.ReleaseGates(gateKeys)
		}})
		cronFn(result)
	}
	req.Repark = func() {
		if !f.done.CompareAndSwap(false, true) {
			return
		}
		d.opts.Loop.Enqueue(&loops.ConnRun{Fn: func() {
			d.repark(f, cronFn, gateKeys)
		}})
	}
	log.Debugf("Dispatching pull trigger %s for actor %s to stream %d (sidecar %s, %d slots left)", req.Job.GetName(), actor.GetId(), streamIDx, p.sidecar, p.credit)
	streamLoop.Enqueue(req)
	return true, ""
}

// pickPool returns the eligible sidecar pool for actorType with the most free
// slots. Ties go to the first found from a rotating start, so equally loaded
// sidecars take turns.
func (d *Dispatcher) pickPool(actorType string) (*pool, bool) {
	pools := d.pools[actorType]
	n := len(pools)
	if n == 0 {
		return nil, false
	}

	d.rotate++
	var best *pool
	for i := range n {
		p := pools[(d.rotate+i)%n]
		if len(p.streams) > 0 && p.credit > 0 && (best == nil || p.credit > best.credit) {
			best = p
		}
	}
	return best, best != nil
}

func (d *Dispatcher) hasCredit(actorType string) bool {
	return slices.ContainsFunc(d.pools[actorType], func(p *pool) bool { return p.credit > 0 })
}

func (d *Dispatcher) poolOf(actorType, id string) *pool {
	for _, p := range d.pools[actorType] {
		if p.sidecar == id {
			return p
		}
	}
	return nil
}

// Drain dispatches as many parked pull triggers as capacity allows, one
// group-fair rotation at a time per actor type, until no sidecar has a free
// slot.
// ponytail: while a per-name gate is full and a sidecar has a free slot,
// every release retries the head of each parked group, O(groups) per
// release; index parked triggers by the gate blocking them if that shows up.
func (d *Dispatcher) Drain() {
	for actorType, q := range d.pending {
		if q.len() == 0 {
			delete(d.pending, actorType)
			continue
		}
		if len(d.pools[actorType]) == 0 {
			// No sidecar can take pull work for this type any more: hand the
			// triggers back to the cron so they retry once one reconnects.
			q.drainAll(func(req *loops.TriggerRequest) {
				d.backlog.add(req, -1)
				req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			})
			delete(d.pending, actorType)
			continue
		}
		typeFull := false
		for d.hasCredit(actorType) && !typeFull && q.tryDispatchOne(func(req *loops.TriggerRequest) bool {
			if typeFull {
				return false
			}
			dispatched, full := d.tryDispatch(req)
			if dispatched {
				d.backlog.add(req, -1)
			}
			// Every group needs the actor type gate, so once it is full
			// the rest of the rotation cannot dispatch either.
			typeFull = full == actorType
			return dispatched
		}) {
		}
		if q.len() == 0 {
			delete(d.pending, actorType)
		}
	}
}
