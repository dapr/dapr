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

package connections

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/stream"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.connections")

var (
	loopFactory = loop.New[loops.EventConn](1024)
	connsCache  = sync.Pool{New: func() any {
		return &connections{
			streams:           make(map[uint64]context.CancelFunc),
			streamLoops:       make(map[uint64]loop.Interface[loops.EventStream]),
			streamPool:        store.New(),
			concurrencyGates:  make(map[string]*concurrencyGate),
			streamGateKeys:    make(map[uint64][]string),
			pullPools:         make(map[string][]*pullPool),
			pullPending:       make(map[string]*pullQueue),
			pullBacklogLabels: make(map[string]*pullBacklogLabels),
		}
	}}
)

type Options struct {
	Cron          api.Interface
	NamespaceLoop loop.Interface[loops.EventNS]

	// PlacementEnabled reports whether this scheduler serves placement.
	PlacementEnabled bool
}

// connections is a control loop that creates and manages stream connections,
// piping trigger requests.
type connections struct {
	cron             api.Interface
	nsLoop           loop.Interface[loops.EventNS]
	loop             loop.Interface[loops.EventConn]
	placementEnabled bool

	// schedulerCount and schedulerIdx are the latest view of cluster
	// membership, updated by SchedulerInfoUpdate events. Only accessed from
	// the connections loop goroutine, so plain fields are race-free.
	// Defaults to {count: 1, idx: 0} until the first event arrives.
	schedulerCount uint32
	schedulerIdx   uint32

	streams          map[uint64]context.CancelFunc
	streamLoops      map[uint64]loop.Interface[loops.EventStream]
	streamIDx        uint64
	streamPool       *store.Store
	concurrencyGates map[string]*concurrencyGate
	streamGateKeys   map[uint64][]string

	// pullPools lists, per actor type, the sidecar slot pools declared through
	// ConcurrencyLimitStream and so eligible for pull dispatch. A sidecar may
	// hold several streams at once (it reconnects when its actor types
	// change), so a pool is keyed by the sidecar's actor address when it
	// reports one and shared by all of that sidecar's streams. pullPending
	// parks pull triggers per actor type while no pool has a free slot.
	// pullRotate offsets the slot search so equally loaded pools take turns.
	pullPools         map[string][]*pullPool
	pullPending       map[string]*pullQueue
	pullBacklogLabels map[string]*pullBacklogLabels
	pullRotate        int

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventConn] {
	conns := connsCache.Get().(*connections)

	conns.cron = opts.Cron
	conns.nsLoop = opts.NamespaceLoop
	conns.placementEnabled = opts.PlacementEnabled
	conns.streamIDx = 0
	conns.schedulerCount = 1
	conns.schedulerIdx = 0

	conns.loop = loopFactory.NewLoop(conns)
	return conns.loop
}

func (c *connections) Handle(ctx context.Context, event loops.EventConn) error {
	switch e := event.(type) {
	case *loops.ConnAdd:
		return c.handleAdd(ctx, e)
	case *loops.ConnCloseStream:
		c.handleCloseStream(e)
	case *loops.TriggerRequest:
		c.handleTriggerRequest(e)
	case *loops.ConcurrencyRelease:
		c.handleConcurrencyRelease(e)
	case *loops.SchedulerInfoUpdate:
		c.handleSchedulerInfoUpdate(e)
	case *loops.Shutdown:
		c.handleShutdown()
	default:
		return fmt.Errorf("unknown connections event type: %T", e)
	}

	return nil
}

// handleSchedulerInfoUpdate applies a new cluster view and drains pending
// queues so newly-available capacity (on an idx rebalance or a shrink) is
// used without waiting for the next release.
func (c *connections) handleSchedulerInfoUpdate(e *loops.SchedulerInfoUpdate) {
	//nolint:gosec // count is guarded to be non-negative by the caller
	c.schedulerCount = max(uint32(e.Count), 1)
	//nolint:gosec // idx is non-negative
	c.schedulerIdx = uint32(e.Idx)

	for key, gate := range c.concurrencyGates {
		if gate.pendingLen() > 0 {
			c.drainPending(key, gate)
		}
	}
	c.drainPullPending()
}

// handleAdd adds a connection to the pool for a given namespace/appID.
func (c *connections) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	streamIDx := c.streamIDx
	c.streamIDx++

	streamLoop, err := stream.New(ctx, stream.Options{
		IDx:           streamIDx,
		Add:           add,
		Cron:          c.cron,
		NamespaceLoop: c.nsLoop,
	})
	if err != nil {
		return err
	}

	c.wg.Go(func() {
		_ = streamLoop.Run(ctx)
	})

	var appID *string
	ts := add.Request.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(add.Request.GetAcceptJobTypes(), schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		appID = new(add.Request.GetAppId())
	}

	c.streams[streamIDx] = c.streamPool.Add(store.Options{
		Loop:         streamLoop,
		AppID:        appID,
		ActorTypes:   add.Request.GetActorTypes(),
		ActorAddress: add.Request.ActorAddress,
	})
	c.streamLoops[streamIDx] = streamLoop

	c.updateConcurrencyLimits(streamIDx, add.Request)

	// A new stream with slots is new pull capacity.
	c.drainPullPending()

	return nil
}

func (c *connections) updateConcurrencyLimits(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	var keys []string
	for _, limit := range req.GetConcurrencyLimits() {
		if limit.GetMaxConcurrent() <= 0 {
			continue
		}
		if s := limit.GetStream(); s != nil && s.GetActorType() != "" {
			// A stream can only execute pull deliveries for actor types it
			// hosts; a sidecar declares its slot count before its workflow
			// actor types are registered, so eligibility follows the hosted
			// types (the stream reconnects when those change).
			if !slices.Contains(req.GetActorTypes(), s.GetActorType()) {
				continue
			}
			key := pullGateKey(streamSidecarID(streamIDx, req.GetActorAddress()), s.GetActorType())
			if slices.Contains(keys, key) {
				continue
			}
			gate, ok := c.concurrencyGates[key]
			if !ok {
				gate = &concurrencyGate{perStream: true}
				c.concurrencyGates[key] = gate
			}
			// The latest declaration from the sidecar wins.
			//nolint:gosec // guarded by <= 0 check above
			gate.globalLimit = uint32(limit.GetMaxConcurrent())
			keys = append(keys, key)
			pool := c.pullPoolFor(s.GetActorType(), key)
			pool.streams = append(pool.streams, streamIDx)
			log.Debugf("Stream %d offers %d pull slots for actor type %s in pool %s (%d eligible pools)", streamIDx, limit.GetMaxConcurrent(), s.GetActorType(), key, len(c.pullPools[s.GetActorType()]))
			continue
		}
		actor := limit.GetActor()
		if actor == nil {
			continue
		}
		key := actor.GetType()
		if limit.Name != nil {
			key += ":" + limit.GetName()
		}
		keys = append(keys, key)
		//nolint:gosec // guarded by <= 0 check above
		if gate, ok := c.concurrencyGates[key]; ok {
			gate.globalLimit = uint32(limit.GetMaxConcurrent())
		} else {
			c.concurrencyGates[key] = &concurrencyGate{globalLimit: uint32(limit.GetMaxConcurrent())}
		}
	}
	c.streamGateKeys[streamIDx] = keys

	c.removeOrphanedGates()
}

// removeOrphanedGates deletes gates that no active stream references.
func (c *connections) removeOrphanedGates() {
	referenced := make(map[string]struct{})
	for _, keys := range c.streamGateKeys {
		for _, key := range keys {
			referenced[key] = struct{}{}
		}
	}

	for key, gate := range c.concurrencyGates {
		if _, ok := referenced[key]; !ok {
			for req := gate.dequeue(); req != nil; req = gate.dequeue() {
				req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			}
			delete(c.concurrencyGates, key)
		}
	}
}

// acquireGates attempts to acquire all gates for the given keys. It returns
// the keys successfully acquired and whether all were acquired. On partial
// success (acquired != nil && !ok) the caller must release the acquired gates.
func (c *connections) acquireGates(gateKeys []string) (acquired []string, ok bool) {
	for _, key := range gateKeys {
		gate := c.concurrencyGates[key]
		if !gate.tryAcquire(c.schedulerCount, c.schedulerIdx) {
			return acquired, false
		}
		acquired = append(acquired, key)
	}
	return acquired, true
}

func (c *connections) releaseGates(keys []string) {
	for _, key := range keys {
		if gate, ok := c.concurrencyGates[key]; ok {
			gate.release()
		}
	}
}

func (c *connections) handleTriggerRequest(req *loops.TriggerRequest) {
	if c.handlePullTrigger(req) {
		return
	}

	streamLoop, ok := c.getStreamLoop(req.Job.GetMetadata())
	if !ok {
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return
	}

	gateKeys := c.gateKeysForTrigger(req)
	if len(gateKeys) == 0 {
		streamLoop.Enqueue(req)
		return
	}

	// Gates are released either by the ConcurrencyRelease event wired in
	// dispatchWithGates (happy path), or by the deferred releaseGates call
	// below on any early return. dispatched is set to true only when we hand
	// the request off to the stream loop.
	var acquired []string
	dispatched := false
	defer func() {
		if !dispatched {
			c.releaseGates(acquired)
		}
	}()

	var gotAll bool
	acquired, gotAll = c.acquireGates(gateKeys)
	if !gotAll {
		throttledKey := gateKeys[len(acquired)]
		monitoring.RecordConcurrencyThrottled(throttledKey)
		primaryGate := c.concurrencyGates[gateKeys[0]]
		if !primaryGate.enqueue(req) {
			req.ResultFn(api.TriggerResponseResult_FAILED)
		} else {
			monitoring.RecordConcurrencyPending(gateKeys[0], int64(primaryGate.pendingLen()))
		}
		return
	}

	c.dispatchWithGates(streamLoop, req, gateKeys)
	dispatched = true
}

func (c *connections) gateKeysForTrigger(req *loops.TriggerRequest) []string {
	meta := req.Job.GetMetadata()

	var typeKey string
	switch t := meta.GetTarget().GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Actor:
		typeKey = t.Actor.GetType()
	default:
		return nil
	}

	var keys []string

	if _, ok := c.concurrencyGates[typeKey]; ok {
		keys = append(keys, typeKey)
	}

	if meta.ConcurrencyKey != nil {
		namedKey := typeKey + ":" + meta.GetConcurrencyKey()
		if _, ok := c.concurrencyGates[namedKey]; ok {
			keys = append(keys, namedKey)
		}
	}

	return keys
}

func (c *connections) dispatchWithGates(streamLoop loop.Interface[loops.EventStream], req *loops.TriggerRequest, gateKeys []string) {
	for _, key := range gateKeys {
		if gate, ok := c.concurrencyGates[key]; ok {
			monitoring.RecordConcurrencyInflight(key, int64(gate.current))
		}
	}
	originalResultFn := req.ResultFn
	req.ResultFn = func(result api.TriggerResponseResult) {
		originalResultFn(result)
		c.loop.Enqueue(&loops.ConcurrencyRelease{
			GateKeys: gateKeys,
		})
	}
	streamLoop.Enqueue(req)
}

func (c *connections) handleConcurrencyRelease(rel *loops.ConcurrencyRelease) {
	for _, key := range rel.GateKeys {
		gate, ok := c.concurrencyGates[key]
		if !ok {
			continue
		}

		gate.release()
		monitoring.RecordConcurrencyInflight(key, int64(gate.current))

		c.drainPending(key, gate)
	}

	// A pull trigger parks behind whichever gate was full (stream slot, type
	// or per-name), so any release may have freed it. Hashed pending is drained
	// first, so behind a shared type or per-name gate pull work waits until the
	// hashed queue for that gate is empty. Pull workloads are low rate by
	// design, so the bias is accepted over interleaving the two queues.
	c.drainPullPending()
}

// handlePullTrigger dispatches a pull-flagged actor reminder trigger into a
// free per-stream slot, or parks it until one frees. It returns false when
// pull dispatch does not apply (not a pull trigger, not an actor target, or no
// connected stream declared slots for the actor type), in which case the
// caller routes the trigger as usual.
func (c *connections) handlePullTrigger(req *loops.TriggerRequest) bool {
	meta := req.Job.GetMetadata()
	if !meta.GetPull() {
		return false
	}
	actor := meta.GetTarget().GetActor()
	if actor == nil || len(c.pullPools[actor.GetType()]) == 0 {
		log.Debugf("Pull trigger %s has no eligible sidecar for its actor type; using regular routing", req.Job.GetName())
		return false
	}

	if c.tryDispatchPull(req) {
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
	monitoring.RecordConcurrencyThrottled(actor.GetType())
	c.recordPullBacklog(actor.GetType())
	return true
}

// tryDispatchPull hands req to the eligible sidecar with the most free slots,
// acquiring that slot together with the trigger's type and per-name gates
// all-or-nothing. Returns false, leaving no gate held, when no sidecar has a
// free slot or a shared gate is full.
func (c *connections) tryDispatchPull(req *loops.TriggerRequest) bool {
	actorType := req.Job.GetMetadata().GetTarget().GetActor().GetType()
	pool, ok := c.pickPullPool(actorType)
	if !ok {
		return false
	}
	// The sidecar's newest stream: an older one may be draining.
	streamIDx := pool.streams[len(pool.streams)-1]
	streamLoop, ok := c.streamLoops[streamIDx]
	if !ok {
		return false
	}

	gateKeys := append([]string{pool.key}, c.gateKeysForTrigger(req)...)
	acquired, gotAll := c.acquireGates(gateKeys)
	if !gotAll {
		c.releaseGates(acquired)
		return false
	}

	log.Debugf("Dispatching pull trigger %s for actor %s to stream %d (pool %s)", req.Job.GetName(), req.Job.GetMetadata().GetTarget().GetActor().GetId(), streamIDx, pool.key)
	c.dispatchWithGates(streamLoop, req, gateKeys)
	return true
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
	best, bestFree := -1, uint32(0)
	for i := range n {
		pos := (c.pullRotate + i) % n
		gate, ok := c.concurrencyGates[pools[pos].key]
		if !ok || len(pools[pos].streams) == 0 {
			continue
		}
		if free := gate.free(c.schedulerCount, c.schedulerIdx); free > bestFree {
			best, bestFree = pos, free
		}
	}
	if best < 0 {
		return nil, false
	}
	return pools[best], true
}

// pullPoolFor returns the pool for gate key under actorType, creating it.
func (c *connections) pullPoolFor(actorType, key string) *pullPool {
	for _, p := range c.pullPools[actorType] {
		if p.key == key {
			return p
		}
	}
	p := &pullPool{key: key}
	c.pullPools[actorType] = append(c.pullPools[actorType], p)
	return p
}

// pullPool is one sidecar's slot pool for one actor type: its gate key in
// concurrencyGates and the sidecar's live streams, in connection order.
type pullPool struct {
	key     string
	streams []uint64
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
				req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
			})
			delete(c.pullPending, actorType)
			c.recordPullBacklog(actorType)
			continue
		}
		for q.tryDispatchOne(c.tryDispatchPull) {
		}
		if q.len() == 0 {
			delete(c.pullPending, actorType)
		}
		c.recordPullBacklog(actorType)
	}
}

// recordPullBacklog publishes the parked pull trigger counts for actorType,
// broken down by activity name (the trigger's concurrency key).
func (c *connections) recordPullBacklog(actorType string) {
	counts := make(map[string]int64)
	var namespace, appID string
	if q, ok := c.pullPending[actorType]; ok {
		for _, key := range q.ring {
			for _, req := range q.byInstance[key] {
				meta := req.Job.GetMetadata()
				namespace, appID = meta.GetNamespace(), meta.GetAppId()
				counts[meta.GetConcurrencyKey()]++
			}
		}
	}
	if len(counts) == 0 {
		// The queue emptied: zero the gauge for names seen while it was
		// non-empty, using the last reported labels for this type.
		if last, ok := c.pullBacklogLabels[actorType]; ok {
			for name := range last.names {
				monitoring.RecordWorkflowActivityBacklog(last.namespace, last.appID, name, 0)
			}
			delete(c.pullBacklogLabels, actorType)
		}
		return
	}
	last := c.pullBacklogLabels[actorType]
	if last == nil {
		last = &pullBacklogLabels{names: make(map[string]struct{})}
		c.pullBacklogLabels[actorType] = last
	}
	last.namespace, last.appID = namespace, appID
	for name := range last.names {
		if _, ok := counts[name]; !ok {
			monitoring.RecordWorkflowActivityBacklog(namespace, appID, name, 0)
			delete(last.names, name)
		}
	}
	for name, count := range counts {
		last.names[name] = struct{}{}
		monitoring.RecordWorkflowActivityBacklog(namespace, appID, name, count)
	}
}

// pullBacklogLabels remembers which activity names a type's backlog gauge has
// reported, so the gauge can be zeroed when a name drains.
type pullBacklogLabels struct {
	namespace string
	appID     string
	names     map[string]struct{}
}

// drainPending scans the pending queue to find a trigger that can acquire all
// required gates. This avoids head-of-line blocking when the first pending
// trigger is blocked on a different gate than the one that just released.
// Requests skipped by the scan are returned to the front of the queue in
// their original order, so an elder trigger is never rotated behind fresher
// arrivals.
func (c *connections) drainPending(key string, gate *concurrencyGate) {
	defer func() {
		monitoring.RecordConcurrencyPending(key, int64(gate.pendingLen()))
	}()

	n := gate.pendingLen()
	var skipped []*loops.TriggerRequest
	for range n {
		next := gate.dequeue()
		if next == nil {
			break
		}

		dispatched, consumed := c.tryDispatchPending(next)
		if !consumed {
			skipped = append(skipped, next)
		}
		if dispatched {
			break
		}
	}

	gate.requeueFront(skipped)
}

// tryDispatchPending attempts to dispatch a single pending trigger. Returns
// dispatched=true iff the trigger was handed to a stream loop, and
// consumed=true when the caller no longer owns the request (dispatched, or
// resolved undeliverable). On gate acquisition failure the request stays with
// the caller to requeue at its original position, and partially-acquired
// gates are released via defer.
func (c *connections) tryDispatchPending(next *loops.TriggerRequest) (dispatched, consumed bool) {
	streamLoop, ok := c.getStreamLoop(next.Job.GetMetadata())
	if !ok {
		next.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return false, true
	}

	gateKeys := c.gateKeysForTrigger(next)

	var acquired []string
	defer func() {
		if !dispatched {
			c.releaseGates(acquired)
		}
	}()

	var gotAll bool
	acquired, gotAll = c.acquireGates(gateKeys)
	if !gotAll {
		return false, false
	}

	c.dispatchWithGates(streamLoop, next, gateKeys)
	return true, true
}

// handleCloseStream handles a close stream request.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) {
	cancel, ok := c.streams[closeStream.StreamIDx]
	if !ok {
		// Close events are deduplicated at the stream, so an unknown index is
		// unexpected, but it must never take down this loop: that tears down
		// every stream in the namespace. Log loudly and tolerate.
		log.Errorf("Ignoring close for unknown stream connection %d in namespace %s", closeStream.StreamIDx, closeStream.Namespace)
		return
	}

	delete(c.streams, closeStream.StreamIDx)
	delete(c.streamLoops, closeStream.StreamIDx)
	delete(c.streamGateKeys, closeStream.StreamIDx)
	cancel()

	for actorType, pools := range c.pullPools {
		kept := make([]*pullPool, 0, len(pools))
		for _, p := range pools {
			p.streams = slices.DeleteFunc(p.streams, func(idx uint64) bool { return idx == closeStream.StreamIDx })
			if len(p.streams) > 0 {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			delete(c.pullPools, actorType)
		} else {
			c.pullPools[actorType] = kept
		}
		log.Debugf("Stream %d closed: %d eligible pull sidecars remain for actor type %s", closeStream.StreamIDx, len(kept), actorType)
	}

	c.removeOrphanedGates()
	c.drainPullPending()

	// This loop owns the authoritative stream set: confirm emptiness to the
	// namespaces loop so it can delete the namespace. Deleting on the
	// namespaces loop's own counting alone is unsafe against stray events.
	if len(c.streams) == 0 {
		c.nsLoop.Enqueue(&loops.ConnCloseNamespace{
			Namespace: closeStream.Namespace,
		})
	}
}

// handleShutdown handles the shutdown of the connections.
func (c *connections) handleShutdown() {
	defer c.wg.Wait()

	for _, cancel := range c.streams {
		cancel()
	}

	clear(c.streams)
	clear(c.streamLoops)

	for _, gate := range c.concurrencyGates {
		for req := gate.dequeue(); req != nil; req = gate.dequeue() {
			req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		}
	}
	for _, q := range c.pullPending {
		q.drainAll(func(req *loops.TriggerRequest) {
			req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		})
	}
	clear(c.concurrencyGates)
	clear(c.streamGateKeys)
	clear(c.pullPools)
	clear(c.pullPending)
	clear(c.pullBacklogLabels)
	c.pullRotate = 0

	// The loop object is deliberately NOT returned to the factory cache:
	// this handler runs inside the loop's own drain, before Close observes
	// completion, so a recycled loop could be reused and rewritten while the
	// closer still reads it (data race, and a lost close signal).
	connsCache.Put(c)
}

// getStreamLoop returns a stream loop from the pool based on the metadata.
func (c *connections) getStreamLoop(meta *schedulerv1pb.JobMetadata) (loop.Interface[loops.EventStream], bool) {
	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return c.streamPool.AppID(meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		// Owner routing is only correct when this scheduler serves
		// placement; the placement service hashes differently.
		if !c.placementEnabled {
			return c.streamPool.ActorType(t.GetActor().GetType())
		}
		// Route the reminder to the placement owner host for this actor ID
		// when host addresses are known; round robin otherwise. A non-owner
		// host forwards to the owner via its own placement table, so a
		// stale or missing table costs one extra hop, never correctness.
		return c.streamPool.ActorHost(t.GetActor().GetType(), t.GetActor().GetId())
	default:
		return nil, false
	}
}
