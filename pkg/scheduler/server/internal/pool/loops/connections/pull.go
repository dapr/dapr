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

package connections

import (
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

// defaultReattachGrace is how long a pull trigger whose stream closed while it
// ran waits for its sidecar to reconnect and report it as still running. A
// sidecar that does keeps the activity, so a live activity is not run twice;
// the activity of a sidecar that does not is delivered again after this long.
const defaultReattachGrace = 30 * time.Second

// pullPool is one sidecar's pull capacity for one actor type on this
// scheduler: the free slots it last reported less those dispatched since, and
// its live streams in connection order.
type pullPool struct {
	sidecar string
	credit  uint32
	streams []uint64
}

// pullSidecar is a sidecar's state on this scheduler: its live stream count
// and the pull triggers dispatched to it that are not yet resolved.
type pullSidecar struct {
	streams int
	flights map[string]*pullFlight
}

// pullClaim is a pull reminder a sidecar listed as running that this
// scheduler has no flight for, such as after this scheduler restarted. When
// the cron fires it again it goes back to that sidecar, or is resolved with
// the result the sidecar already sent.
// ponytail: a claim for a reminder that never fires again (its workflow was
// purged) lasts until its sidecar disconnects; bounded by the activities in
// flight at a restart.
type pullClaim struct {
	sidecar string
	status  *schedulerv1pb.WatchJobsRequestResultStatus
}

// pullFlight is a pull trigger dispatched to a sidecar and not yet resolved.
// done is set once by whichever of the result, repark or expiry gets there
// first; the hooks run on stream goroutines, everything else on the loop.
type pullFlight struct {
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

func reminderKey(actorType, actorID, name string) string {
	return actorType + "||" + actorID + "||" + name
}

// handlePullTrigger dispatches a pull-flagged actor reminder trigger to a
// sidecar with a free slot, or parks it until one frees. It returns false
// when pull dispatch does not apply (not a pull trigger, not an actor target,
// or no connected sidecar reports slots for the actor type), in which case
// the caller routes the trigger as usual.
func (c *connections) handlePullTrigger(req *loops.TriggerRequest) bool {
	meta := req.Job.GetMetadata()
	if !meta.GetPull() {
		return false
	}
	actor := meta.GetTarget().GetActor()
	if actor == nil {
		return false
	}
	if key, claim := c.claimFor(req); claim != nil && claim.status != nil {
		delete(c.pullClaims, key)
		if *claim.status == schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS {
			req.ResultFn(api.TriggerResponseResult_SUCCESS)
		} else {
			req.ResultFn(api.TriggerResponseResult_FAILED)
		}
		return true
	}
	if len(c.pullPools[actor.GetType()]) == 0 {
		log.Debugf("Pull trigger %s has no eligible sidecar for its actor type; using regular routing", req.Job.GetName())
		// Delivered as a regular reminder, the receiving sidecar forwards it
		// to the placement owner instead of running it as a pull delivery.
		meta.Pull = nil
		return false
	}

	dispatched, throttledKey := c.tryDispatchPull(req)
	if dispatched {
		return true
	}

	q, ok := c.pullPending[actor.GetType()]
	if !ok {
		q = newPullQueue()
		c.pullPending[actor.GetType()] = q
	}
	if !q.enqueue(req) {
		req.ResultFn(api.TriggerResponseResult_FAILED)
		return true
	}
	log.Debugf("Parked pull trigger %s for actor %s: no free slot on %d eligible sidecars (%d waiting)", req.Job.GetName(), actor.GetId(), len(c.pullPools[actor.GetType()]), q.len())
	// Waiting for a sidecar slot is the backlog gauge's job; only a shared
	// type or per-name gate counts as a concurrency throttle.
	if throttledKey != "" {
		monitoring.RecordConcurrencyThrottled(throttledKey)
	}
	c.pullBacklog.add(req, 1)
	return true
}

// tryDispatchPull hands req to the eligible sidecar with the most free slots
// once the trigger's type and per-name gates are acquired. When it does not
// dispatch it leaves no gate held and returns the gate key that was full, or
// "" when no sidecar had a free slot.
// A reminder a sidecar claimed as running only goes back to that sidecar,
// without taking a slot, so it joins the running activity.
func (c *connections) tryDispatchPull(req *loops.TriggerRequest) (dispatched bool, throttledKey string) {
	actorType := req.Job.GetMetadata().GetTarget().GetActor().GetType()
	if key, claim := c.claimFor(req); claim != nil {
		pool := c.pullPoolOf(actorType, claim.sidecar)
		if pool == nil {
			return false, ""
		}
		dispatched, throttledKey = c.dispatchPull(req, pool, false)
		if dispatched {
			delete(c.pullClaims, key)
		}
		return dispatched, throttledKey
	}
	pool, ok := c.pickPullPool(actorType)
	if !ok {
		return false, ""
	}
	return c.dispatchPull(req, pool, true)
}

func (c *connections) claimFor(req *loops.TriggerRequest) (string, *pullClaim) {
	actor := req.Job.GetMetadata().GetTarget().GetActor()
	key := reminderKey(actor.GetType(), actor.GetId(), req.Job.GetName())
	return key, c.pullClaims[key]
}

// dispatchPull hands req to pool's newest stream once its type and per-name
// gates are acquired, taking one of the pool's slots when takeSlot, and
// tracks it until resolved. When it does not dispatch it leaves no gate held
// and returns the gate key that was full.
func (c *connections) dispatchPull(req *loops.TriggerRequest, pool *pullPool, takeSlot bool) (dispatched bool, throttledKey string) {
	// The sidecar's newest stream: an older one may be draining.
	streamIDx := pool.streams[len(pool.streams)-1]
	streamLoop, ok := c.streamLoops[streamIDx]
	if !ok {
		return false, ""
	}
	gateKeys := c.gateKeysForTrigger(req)
	acquired, gotAll := c.acquireGates(gateKeys)
	if !gotAll {
		c.releaseGates(acquired)
		return false, gateKeys[len(acquired)]
	}
	if takeSlot {
		pool.credit--
	}
	for _, key := range gateKeys {
		monitoring.RecordConcurrencyInflight(key, int64(c.concurrencyGates[key].current))
	}

	actor := req.Job.GetMetadata().GetTarget().GetActor()
	f := &pullFlight{
		sidecar:   pool.sidecar,
		actorType: actor.GetType(),
		key:       reminderKey(actor.GetType(), actor.GetId(), req.Job.GetName()),
		req:       req,
		gateKeys:  gateKeys,
	}
	c.pullSidecarFor(pool.sidecar).flights[f.key] = f
	c.pullInflight.add(req, 1)

	cronFn := req.ResultFn
	req.ResultFn = func(result api.TriggerResponseResult) {
		if !f.done.CompareAndSwap(false, true) {
			return
		}
		cronFn(result)
		c.loop.Enqueue(&loops.ConnRun{Fn: func() {
			c.endPullFlight(f)
			c.handleConcurrencyRelease(&loops.ConcurrencyRelease{GateKeys: gateKeys})
		}})
	}
	req.Repark = func() {
		if !f.done.CompareAndSwap(false, true) {
			return
		}
		c.loop.Enqueue(&loops.ConnRun{Fn: func() {
			c.repark(f, cronFn, gateKeys)
		}})
	}
	req.Orphan = func() {
		if f.done.Load() {
			return
		}
		c.loop.Enqueue(&loops.ConnRun{Fn: func() {
			c.orphanPullFlight(f)
		}})
	}

	log.Debugf("Dispatching pull trigger %s for actor %s to stream %d (sidecar %s, %d slots left)", req.Job.GetName(), actor.GetId(), streamIDx, pool.sidecar, pool.credit)
	streamLoop.Enqueue(req)
	return true, ""
}

// repark queues a trigger the sidecar refused for lack of a slot again,
// without resolving it to the cron. The sidecar was full, so its credit is
// zero until it reports otherwise.
func (c *connections) repark(f *pullFlight, cronFn func(api.TriggerResponseResult), gateKeys []string) {
	c.endPullFlight(f)
	if pool := c.pullPoolOf(f.actorType, f.sidecar); pool != nil {
		pool.credit = 0
	}
	// A fresh request: a closed stream may still hold the refused one, whose
	// hooks are now no-ops.
	c.handleTriggerRequest(&loops.TriggerRequest{Job: f.req.Job, ResultFn: cronFn})
	c.handleConcurrencyRelease(&loops.ConcurrencyRelease{GateKeys: gateKeys})
}

// orphanPullFlight marks a flight whose stream closed. It is redelivered after
// reattachGrace unless a stream of the sidecar reports it as running.
func (c *connections) orphanPullFlight(f *pullFlight) {
	if f.done.Load() || !c.isCurrentFlight(f) {
		return
	}
	f.orphaned = true
	if sc := c.pullSidecars[f.sidecar]; !f.confirmed || sc.streams == 0 {
		f.confirmed = false
		c.startPullTimer(f)
	}
}

func (c *connections) startPullTimer(f *pullFlight) {
	if f.timer != nil {
		return
	}
	var t *time.Timer
	t = time.AfterFunc(c.reattachGrace, func() {
		c.loop.Enqueue(&loops.ConnRun{Fn: func() {
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

func (f *pullFlight) stopTimer() {
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
}

func (c *connections) isCurrentFlight(f *pullFlight) bool {
	sc, ok := c.pullSidecars[f.sidecar]
	return ok && sc.flights[f.key] == f
}

// endPullFlight forgets a resolved or reparked flight.
func (c *connections) endPullFlight(f *pullFlight) {
	f.stopTimer()
	if !c.isCurrentFlight(f) {
		return
	}
	delete(c.pullSidecars[f.sidecar].flights, f.key)
	c.pullInflight.add(f.req, -1)
	c.maybeDropPullSidecar(f.sidecar)
	c.maybeCloseNamespace()
}

// hasPullFlights reports whether a pull trigger dispatched by this loop is
// still unresolved. The loop is kept while it does, even with no stream
// connected, so the sidecar can reattach.
func (c *connections) hasPullFlights() bool {
	for _, sc := range c.pullSidecars {
		if len(sc.flights) > 0 {
			return true
		}
	}
	return false
}

// markPullGates adds the gates held by unresolved pull triggers to
// referenced: they outlive the stream that delivered them, and dropping
// their gates would let a reconnect start counting again from zero.
func (c *connections) markPullGates(referenced map[string]struct{}) {
	for _, sc := range c.pullSidecars {
		for _, f := range sc.flights {
			for _, key := range f.gateKeys {
				referenced[key] = struct{}{}
			}
		}
	}
}

func (c *connections) pullSidecarFor(sidecar string) *pullSidecar {
	sc, ok := c.pullSidecars[sidecar]
	if !ok {
		sc = &pullSidecar{flights: make(map[string]*pullFlight)}
		c.pullSidecars[sidecar] = sc
	}
	return sc
}

func (c *connections) maybeDropPullSidecar(sidecar string) {
	if sc, ok := c.pullSidecars[sidecar]; ok && sc.streams == 0 && len(sc.flights) == 0 {
		delete(c.pullSidecars, sidecar)
	}
}

// addPullStream records a new stream's sidecar, the pull slots it offers for
// the actor types it hosts, and the pull reminders it reports as running.
func (c *connections) addPullStream(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	sidecar := loops.SidecarID(streamIDx, req.GetActorAddress())
	c.streamSidecars[streamIDx] = sidecar
	sc := c.pullSidecarFor(sidecar)
	sc.streams++

	listed := make(map[string]struct{}, len(req.GetInflightReminders()))
	for _, r := range req.GetInflightReminders() {
		listed[reminderKey(r.GetActorType(), r.GetActorId(), r.GetName())] = struct{}{}
	}
	for _, f := range sc.flights {
		if _, ok := listed[f.key]; ok {
			f.confirmed = true
			f.stopTimer()
			delete(listed, f.key)
		} else if f.orphaned {
			f.confirmed = false
			c.startPullTimer(f)
		}
	}
	// The sidecar's latest listing replaces its running claims; a claim
	// already holding the sidecar's result is kept until the cron fires it.
	for key, claim := range c.pullClaims {
		if claim.sidecar == sidecar && claim.status == nil {
			delete(c.pullClaims, key)
		}
	}
	for key := range listed {
		if _, ok := c.pullClaims[key]; !ok {
			c.pullClaims[key] = &pullClaim{sidecar: sidecar}
		}
	}

	for _, ps := range req.GetPullSlots() {
		// A sidecar reports slots before its workflow actor types are
		// registered; it is eligible once it hosts the type (the stream
		// reconnects when those change).
		if !slices.Contains(req.GetActorTypes(), ps.GetActorType()) {
			continue
		}
		pool := c.pullPoolOf(ps.GetActorType(), sidecar)
		if pool == nil {
			pool = &pullPool{sidecar: sidecar}
			c.pullPools[ps.GetActorType()] = append(c.pullPools[ps.GetActorType()], pool)
		}
		pool.credit = ps.GetFree()
		pool.streams = append(pool.streams, streamIDx)
		log.Debugf("Stream %d of sidecar %s offers %d pull slots for actor type %s (%d eligible sidecars)", streamIDx, sidecar, ps.GetFree(), ps.GetActorType(), len(c.pullPools[ps.GetActorType()]))
	}
}

// closePullStream removes a closed stream from the pull pools. When it was the
// sidecar's last stream, its orphaned flights start their reattach timers.
func (c *connections) closePullStream(streamIDx uint64) {
	for actorType, pools := range c.pullPools {
		pools = slices.DeleteFunc(pools, func(p *pullPool) bool {
			p.streams = slices.DeleteFunc(p.streams, func(idx uint64) bool { return idx == streamIDx })
			return len(p.streams) == 0
		})
		if len(pools) == 0 {
			delete(c.pullPools, actorType)
		} else {
			c.pullPools[actorType] = pools
		}
	}

	sidecar, ok := c.streamSidecars[streamIDx]
	if !ok {
		return
	}
	delete(c.streamSidecars, streamIDx)
	sc := c.pullSidecars[sidecar]
	sc.streams--
	if sc.streams == 0 {
		for key, claim := range c.pullClaims {
			if claim.sidecar == sidecar {
				delete(c.pullClaims, key)
			}
		}
		for _, f := range sc.flights {
			if f.orphaned {
				f.confirmed = false
				c.startPullTimer(f)
			}
		}
	}
	c.maybeDropPullSidecar(sidecar)
}

func (c *connections) handlePullSlotsUpdate(e *loops.PullSlotsUpdate) {
	pool := c.pullPoolOf(e.ActorType, e.Sidecar)
	if pool == nil {
		return
	}
	grew := e.Free > pool.credit
	pool.credit = e.Free
	if grew {
		c.drainPullPending()
	}
}

// handlePullResult resolves a pull reminder whose result the sidecar sent by
// reference because the stream that delivered it closed.
func (c *connections) handlePullResult(e *loops.PullResult) {
	r := e.Reminder
	key := reminderKey(r.GetActorType(), r.GetActorId(), r.GetName())
	var f *pullFlight
	if sc, ok := c.pullSidecars[e.Sidecar]; ok {
		f = sc.flights[key]
	}
	if f == nil {
		// A claimed reminder has no trigger to resolve yet: keep the result
		// for when the cron fires it again.
		if claim, ok := c.pullClaims[key]; ok && claim.sidecar == e.Sidecar {
			if e.Status == schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY {
				delete(c.pullClaims, key)
			} else {
				claim.status = &e.Status
			}
			return
		}
		log.Debugf("Dropping result for unknown pull reminder %s from sidecar %s", key, e.Sidecar)
		return
	}
	switch e.Status {
	case schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS:
		f.req.ResultFn(api.TriggerResponseResult_SUCCESS)
	case schedulerv1pb.WatchJobsRequestResultStatus_NO_CAPACITY:
		f.req.Repark()
	default:
		f.req.ResultFn(api.TriggerResponseResult_FAILED)
	}
}

// pickPullPool returns the eligible sidecar pool for actorType with the most
// free slots. Ties go to the first found from a rotating start, so equally
// loaded sidecars take turns.
func (c *connections) pickPullPool(actorType string) (*pullPool, bool) {
	pools := c.pullPools[actorType]
	n := len(pools)
	if n == 0 {
		return nil, false
	}

	c.pullRotate++
	var best *pullPool
	for i := range n {
		p := pools[(c.pullRotate+i)%n]
		if len(p.streams) > 0 && p.credit > 0 && (best == nil || p.credit > best.credit) {
			best = p
		}
	}
	return best, best != nil
}

func (c *connections) pullPoolOf(actorType, sidecar string) *pullPool {
	for _, p := range c.pullPools[actorType] {
		if p.sidecar == sidecar {
			return p
		}
	}
	return nil
}

// drainPullPending dispatches as many parked pull triggers as capacity allows,
// one instance-fair rotation at a time per actor type.
func (c *connections) drainPullPending() {
	for actorType, q := range c.pullPending {
		if q.len() == 0 {
			delete(c.pullPending, actorType)
			continue
		}
		if len(c.pullPools[actorType]) == 0 {
			// No sidecar can take pull work for this type any more: hand the
			// triggers back to the cron so they retry once one reconnects.
			q.drainAll(func(req *loops.TriggerRequest) {
				c.pullBacklog.add(req, -1)
				req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			})
			delete(c.pullPending, actorType)
			continue
		}
		if len(c.pullClaims) == 0 && !slices.ContainsFunc(c.pullPools[actorType], func(p *pullPool) bool { return p.credit > 0 }) {
			continue
		}
		for q.tryDispatchOne(func(req *loops.TriggerRequest) bool {
			dispatched, _ := c.tryDispatchPull(req)
			if dispatched {
				c.pullBacklog.add(req, -1)
			}
			return dispatched
		}) {
		}
		if q.len() == 0 {
			delete(c.pullPending, actorType)
		}
	}
}

// shutdownPull resolves every parked and dispatched pull trigger
// UNDELIVERABLE. Called after every stream is closed.
func (c *connections) shutdownPull() {
	for _, q := range c.pullPending {
		q.drainAll(func(req *loops.TriggerRequest) {
			c.pullBacklog.add(req, -1)
			req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		})
	}
	for _, sc := range c.pullSidecars {
		for _, f := range sc.flights {
			f.stopTimer()
			f.req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			// The loop is closing, so the flight's own end never runs.
			c.pullInflight.add(f.req, -1)
		}
	}
	clear(c.pullPools)
	clear(c.pullPending)
	clear(c.pullClaims)
	clear(c.pullSidecars)
	clear(c.streamSidecars)
	c.pullRotate = 0
}

// pullGauge counts pull triggers by app and activity name (the trigger's
// concurrency key) and publishes each count as it changes.
type pullGauge struct {
	record func(namespace, appID, activityName string, count int64)
	counts map[pullGaugeKey]int64
}

type pullGaugeKey struct {
	appID, name string
}

func newPullGauge(record func(namespace, appID, activityName string, count int64)) *pullGauge {
	return &pullGauge{record: record, counts: make(map[pullGaugeKey]int64)}
}

func (g *pullGauge) add(req *loops.TriggerRequest, delta int64) {
	meta := req.Job.GetMetadata()
	key := pullGaugeKey{appID: meta.GetAppId(), name: meta.GetConcurrencyKey()}
	g.counts[key] += delta
	g.record(meta.GetNamespace(), key.appID, key.name, g.counts[key])
	if g.counts[key] == 0 {
		delete(g.counts, key)
	}
}

// pullQueue parks pull-dispatched triggers of one actor type while no
// sidecar has a free slot. Triggers are grouped by workflow instance and
// served round robin across instances, so one instance with a large fan-out
// cannot starve the others.
type pullQueue struct {
	byInstance map[string][]*loops.TriggerRequest
	ring       []string
	next       int
	n          int
}

func newPullQueue() *pullQueue {
	return &pullQueue{byInstance: make(map[string][]*loops.TriggerRequest)}
}

// pullInstanceKey returns the workflow instance ID encoded in an activity
// actor ID "<instanceID>::<taskID>::<generation>". The instance ID may itself
// contain the separator, so the last two components are cut. Any other actor
// ID is its own key.
func pullInstanceKey(actorID string) string {
	if i := strings.LastIndex(actorID, "::"); i >= 0 {
		if j := strings.LastIndex(actorID[:i], "::"); j >= 0 {
			return actorID[:j]
		}
	}
	return actorID
}

func (q *pullQueue) len() int {
	return q.n
}

func (q *pullQueue) enqueue(req *loops.TriggerRequest) bool {
	if q.n >= maxPendingPerGate {
		return false
	}
	key := pullInstanceKey(req.Job.GetMetadata().GetTarget().GetActor().GetId())
	if _, ok := q.byInstance[key]; !ok {
		q.ring = append(q.ring, key)
	}
	q.byInstance[key] = append(q.byInstance[key], req)
	q.n++
	return true
}

// tryDispatchOne offers the head trigger of each instance, in ring order
// starting after the last served instance, to fn until one is accepted. The
// accepted trigger is removed and the rotation advances past its instance.
// Returns false when no head was accepted in a full rotation.
func (q *pullQueue) tryDispatchOne(fn func(*loops.TriggerRequest) bool) bool {
	for i := range len(q.ring) {
		pos := (q.next + i) % len(q.ring)
		key := q.ring[pos]
		reqs := q.byInstance[key]
		if !fn(reqs[0]) {
			continue
		}

		reqs[0] = nil
		if len(reqs) == 1 {
			delete(q.byInstance, key)
			q.ring = append(q.ring[:pos], q.ring[pos+1:]...)
			if len(q.ring) == 0 {
				q.next = 0
			} else {
				q.next = pos % len(q.ring)
			}
		} else {
			q.byInstance[key] = reqs[1:]
			q.next = (pos + 1) % len(q.ring)
		}
		q.n--
		return true
	}
	return false
}

// drainAll removes every parked trigger, passing each to fn.
func (q *pullQueue) drainAll(fn func(*loops.TriggerRequest)) {
	for _, key := range q.ring {
		for _, req := range q.byInstance[key] {
			fn(req)
		}
	}
	q.byInstance = make(map[string][]*loops.TriggerRequest)
	q.ring = nil
	q.next = 0
	q.n = 0
}
