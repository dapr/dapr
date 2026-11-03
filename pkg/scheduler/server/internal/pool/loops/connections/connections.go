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
	"strings"
	"sync"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	"k8s.io/utils/clock"

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
		return new(connections)
	}}
)

type Options struct {
	Cron          api.Interface
	NamespaceLoop loop.Interface[loops.EventNS]

	// PlacementEnabled reports whether this scheduler serves placement.
	PlacementEnabled bool

	// Namespace is the namespace this loop serves.
	Namespace string

	// Adaptive enables adaptive concurrency limits for the namespace and for
	// its workflow and activity actor types.
	Adaptive bool

	// Clock is used for adaptive limits. Defaults to the real clock.
	Clock clock.PassiveClock
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

	adaptive bool
	clock    clock.PassiveClock
	now      time.Time
	nextEval time.Time

	// internalPrefix is the prefix of this namespace's internal actor types,
	// and nsKey names the namespace gate. The namespace gate is held apart
	// from concurrencyGates so no actor type can collide with it.
	internalPrefix string
	nsKey          string
	nsGate         *concurrencyGate

	streams          map[uint64]context.CancelFunc
	streamIDx        uint64
	streamPool       *store.Store
	concurrencyGates map[string]*concurrencyGate
	streamGateKeys   map[uint64][]string

	// niceness is keyed by app, or by actor type and flow name.
	niceness           map[string]int32
	streamNicenessKeys map[uint64][]string

	// hostLimits holds the per-sidecar limit each host advertised, by actor
	// type then host address.
	hostLimits  map[string]map[string]*hostLimit
	streamHosts map[uint64]streamHost

	pending    pendingNode
	pendingSeq uint64

	wg sync.WaitGroup
}

type hostLimit struct {
	limit uint32
	refs  int
}

type streamHost struct {
	addr       string
	actorTypes []string
}

func New(opts Options) loop.Interface[loops.EventConn] {
	conns := connsCache.Get().(*connections)
	conns.reset(opts)
	conns.loop = loopFactory.NewLoop(conns)
	return conns.loop
}

func (c *connections) reset(opts Options) {
	c.cron = opts.Cron
	c.nsLoop = opts.NamespaceLoop
	c.placementEnabled = opts.PlacementEnabled
	c.streamIDx = 0
	c.schedulerCount = 1
	c.schedulerIdx = 0

	c.adaptive = opts.Adaptive
	c.clock = opts.Clock
	if c.clock == nil {
		c.clock = clock.RealClock{}
	}
	c.now = c.clock.Now()
	c.nextEval = time.Time{}
	c.internalPrefix = "dapr.internal." + opts.Namespace + "."
	c.nsKey = "dapr.internal." + opts.Namespace
	c.nsGate = nil
	if opts.Adaptive {
		c.nsGate = &concurrencyGate{est: newEstimator(c.now, 2)}
	}

	c.streams = make(map[uint64]context.CancelFunc)
	c.streamPool = store.New()
	c.concurrencyGates = make(map[string]*concurrencyGate)
	c.streamGateKeys = make(map[uint64][]string)
	c.niceness = make(map[string]int32)
	c.streamNicenessKeys = make(map[uint64][]string)
	c.hostLimits = make(map[string]map[string]*hostLimit)
	c.streamHosts = make(map[uint64]streamHost)
	c.pending = pendingNode{}
	c.pendingSeq = 0
}

func (c *connections) Handle(ctx context.Context, event loops.EventConn) error {
	if c.clock != nil {
		c.now = c.clock.Now()
	}

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
// triggers so newly-available capacity (on an idx rebalance or a shrink) is
// used without waiting for the next release.
func (c *connections) handleSchedulerInfoUpdate(e *loops.SchedulerInfoUpdate) {
	//nolint:gosec // count is guarded to be non-negative by the caller
	c.schedulerCount = max(uint32(e.Count), 1)
	//nolint:gosec // idx is non-negative
	c.schedulerIdx = uint32(e.Idx)

	c.recordLimits()
	c.drain()
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

	c.registerStream(streamIDx, add.Request)
	c.drain()

	return nil
}

// registerStream applies the concurrency limits, adaptive gates, advertised
// per-sidecar limits and niceness a stream declares. A value declared by the
// most recently connected stream wins.
func (c *connections) registerStream(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	var keys []string

	for _, limit := range req.GetConcurrencyLimits() {
		actor := limit.GetActor()
		if limit.GetMaxConcurrent() <= 0 || actor == nil {
			continue
		}
		key := actor.GetType()
		if limit.Name != nil {
			key += ":" + limit.GetName()
		}
		gate := c.ensureGate(key)
		if gate == nil {
			continue
		}
		//nolint:gosec // guarded by <= 0 check above
		gate.globalLimit = uint32(limit.GetMaxConcurrent())
		keys = append(keys, key)
	}

	if c.adaptive {
		for _, actorType := range req.GetActorTypes() {
			if _, ok := c.workflowApp(actorType); !ok {
				continue
			}
			gate := c.ensureGate(actorType)
			if gate == nil {
				continue
			}
			if gate.est == nil {
				gate.est = newEstimator(c.now, 1)
				c.nextEval = time.Time{}
			}
			keys = append(keys, actorType)
		}
	}

	if addr := req.GetActorAddress(); addr != "" {
		sh := streamHost{addr: addr}
		for _, limit := range req.GetHostConcurrencyLimits() {
			actor := limit.GetActor()
			if limit.GetMaxConcurrent() <= 0 || actor == nil || limit.Name != nil {
				continue
			}
			actorType := actor.GetType()
			if c.ensureGate(actorType) == nil {
				continue
			}
			hosts, ok := c.hostLimits[actorType]
			if !ok {
				hosts = make(map[string]*hostLimit)
				c.hostLimits[actorType] = hosts
			}
			hl, ok := hosts[addr]
			if !ok {
				hl = new(hostLimit)
				hosts[addr] = hl
			}
			//nolint:gosec // guarded by <= 0 check above
			hl.limit = uint32(limit.GetMaxConcurrent())
			hl.refs++
			sh.actorTypes = append(sh.actorTypes, actorType)
			keys = append(keys, actorType)
		}
		c.streamHosts[streamIDx] = sh
	}

	c.streamGateKeys[streamIDx] = keys

	c.streamNicenessKeys[streamIDx] = c.registerNiceness(req)

	c.refreshHostLimits()
	c.removeOrphans()
	c.recordLimits()
}

// registerNiceness records a stream's workflow niceness, keyed by its app and
// by its workflow and activity actor types joined with each name, and returns
// the keys it set.
func (c *connections) registerNiceness(req *schedulerv1pb.WatchJobsRequestInitial) []string {
	niceness := req.GetNiceness()
	if niceness == nil || req.GetAppId() == "" {
		return nil
	}

	app := c.internalPrefix + req.GetAppId()
	keys := make([]string, 0, 1+len(niceness.GetWorkflows())+len(niceness.GetActivities()))
	set := func(key string, value int32) {
		c.niceness[key] = value
		keys = append(keys, key)
		monitoring.RecordConcurrencyNiceness(key, value)
	}

	set(app, niceness.GetDefault())
	for name, value := range niceness.GetWorkflows() {
		set(app+".workflow:"+name, value)
	}
	for name, value := range niceness.GetActivities() {
		set(app+".activity:"+name, value)
	}
	return keys
}

func (c *connections) unregisterStream(streamIDx uint64) {
	delete(c.streamGateKeys, streamIDx)
	delete(c.streamNicenessKeys, streamIDx)

	if sh, ok := c.streamHosts[streamIDx]; ok {
		for _, actorType := range sh.actorTypes {
			hosts := c.hostLimits[actorType]
			if hl, ok := hosts[sh.addr]; ok {
				hl.refs--
				if hl.refs <= 0 {
					delete(hosts, sh.addr)
				}
			}
			if len(hosts) == 0 {
				delete(c.hostLimits, actorType)
			}
		}
		delete(c.streamHosts, streamIDx)
	}

	c.refreshHostLimits()
	c.removeOrphans()
	c.recordLimits()
}

// ensureGate returns the gate for key, creating it. The namespace gate key
// is reserved, so a gate is never created for it.
func (c *connections) ensureGate(key string) *concurrencyGate {
	if key == c.nsKey {
		log.Warnf("Ignoring concurrency limit for %q: the key is reserved for the namespace", key)
		return nil
	}
	gate, ok := c.concurrencyGates[key]
	if !ok {
		gate = new(concurrencyGate)
		c.concurrencyGates[key] = gate
	}
	return gate
}

func (c *connections) gate(key string) *concurrencyGate {
	if c.nsGate != nil && key == c.nsKey {
		return c.nsGate
	}
	return c.concurrencyGates[key]
}

func (c *connections) refreshHostLimits() {
	for key, gate := range c.concurrencyGates {
		var sum uint32
		for _, hl := range c.hostLimits[key] {
			sum += hl.limit
		}
		gate.hostLimit = sum
	}
}

// removeOrphans deletes gates and niceness no active stream references, and
// resolves the pending triggers of actor types no stream hosts any more.
func (c *connections) removeOrphans() {
	referenced := make(map[string]struct{})
	for _, keys := range c.streamGateKeys {
		for _, key := range keys {
			referenced[key] = struct{}{}
		}
	}
	for key, gate := range c.concurrencyGates {
		if _, ok := referenced[key]; !ok {
			delete(c.concurrencyGates, key)
			monitoring.RecordConcurrencyLimit(key, 0)
			if gate.est != nil {
				monitoring.RecordConcurrencyEstimate(key, 0, 0)
			}
		}
	}

	clear(referenced)
	for _, keys := range c.streamNicenessKeys {
		for _, key := range keys {
			referenced[key] = struct{}{}
		}
	}
	for key := range c.niceness {
		if _, ok := referenced[key]; !ok {
			delete(c.niceness, key)
			// Without an entry the niceness in effect is the default.
			monitoring.RecordConcurrencyNiceness(key, 0)
		}
	}

	c.failPending(c.streamPool.HasActorType, api.TriggerResponseResult_UNDELIVERABLE)
}

// workflowApp returns the app key of one of this namespace's internal
// workflow or activity actor types, "dapr.internal.<ns>.<appID>". App IDs
// cannot contain dots, so the parse is unambiguous.
func (c *connections) workflowApp(actorType string) (string, bool) {
	rest, ok := strings.CutPrefix(actorType, c.internalPrefix)
	if !ok {
		return "", false
	}
	app, ok := strings.CutSuffix(rest, ".workflow")
	if !ok {
		app, ok = strings.CutSuffix(rest, ".activity")
	}
	if !ok || app == "" || strings.Contains(app, ".") {
		return "", false
	}
	return c.internalPrefix + app, true
}

// appKey groups an actor type under its app in the pending tree. Actor types
// other than workflows and activities are their own app.
func (c *connections) appKey(actorType string) string {
	if app, ok := c.workflowApp(actorType); ok {
		return app
	}
	return actorType
}

// acquireGates takes a slot on every gate or on none. On refusal it marks the
// first refusing gate as binding and returns its key.
func (c *connections) acquireGates(gateKeys []string) (string, bool) {
	for i, key := range gateKeys {
		gate := c.gate(key)
		if gate == nil {
			continue
		}
		if !gate.tryAcquire(c.schedulerCount, c.schedulerIdx) {
			c.releaseGates(gateKeys[:i])
			gate.markBound(c.now)
			return key, false
		}
	}
	return "", true
}

func (c *connections) releaseGates(keys []string) {
	for _, key := range keys {
		if gate := c.gate(key); gate != nil {
			gate.release()
		}
	}
}

func (c *connections) handleTriggerRequest(req *loops.TriggerRequest) {
	c.evaluate()

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

	// A trigger never overtakes earlier triggers of its own flow.
	if c.pendingFlow(c.flowPath(req)) != nil {
		monitoring.RecordConcurrencyThrottled(gateKeys[0])
	} else if refused, ok := c.acquireGates(gateKeys); ok {
		c.dispatchWithGates(streamLoop, req, gateKeys, 0)
		return
	} else {
		monitoring.RecordConcurrencyThrottled(refused)
	}

	c.enqueuePending(req)
}

// gateKeysForTrigger returns the gates a trigger must pass, in order: the
// namespace gate, the actor type gate, and the actor type and name gate.
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

	if c.nsGate != nil {
		if _, ok := c.workflowApp(typeKey); ok {
			keys = append(keys, c.nsKey)
		}
	}

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

// dispatchWithGates hands a trigger holding its gates to its stream. wait is
// how long it waited for them.
func (c *connections) dispatchWithGates(streamLoop loop.Interface[loops.EventStream], req *loops.TriggerRequest, gateKeys []string, wait time.Duration) {
	meta := req.Job.GetMetadata()
	actorType := meta.GetTarget().GetActor().GetType()
	name := meta.GetConcurrencyKey()

	for _, key := range gateKeys {
		if gate := c.gate(key); gate != nil {
			monitoring.RecordConcurrencyInflight(key, int64(gate.current))
		}
		monitoring.RecordConcurrencyDispatched(key)
	}
	if name != "" && !slices.Contains(gateKeys, actorType+":"+name) {
		monitoring.RecordConcurrencyDispatched(actorType + ":" + name)
	}
	monitoring.RecordConcurrencyWait(actorType, wait)
	if name != "" {
		monitoring.RecordConcurrencyWait(actorType+":"+name, wait)
	}

	sent := c.now
	originalResultFn := req.ResultFn
	req.ResultFn = func(result api.TriggerResponseResult) {
		originalResultFn(result)
		c.loop.Enqueue(&loops.ConcurrencyRelease{
			GateKeys:  gateKeys,
			ActorType: actorType,
			Name:      name,
			Sent:      sent,
			Result:    result,
		})
	}
	streamLoop.Enqueue(req)
}

func (c *connections) handleConcurrencyRelease(rel *loops.ConcurrencyRelease) {
	success := rel.Result == api.TriggerResponseResult_SUCCESS

	for _, key := range rel.GateKeys {
		gate := c.gate(key)
		if gate == nil {
			continue
		}

		gate.release()
		if gate.est != nil && success {
			gate.est.acks++
		}
		monitoring.RecordConcurrencyInflight(key, int64(gate.current))
	}

	// Fast failures say nothing about capacity, so only successful
	// acknowledgements contribute round trip times.
	if gate := c.concurrencyGates[rel.ActorType]; success && gate != nil && gate.est != nil {
		gate.est.record(rel.Name, rel.Sent, c.now)
	}

	c.evaluate()
	c.drain()
}

// evaluate runs, at most once per minEvalPeriod, every adaptive estimator
// whose window has elapsed. Every actor type's current latency ratio is
// computed first so the namespace sees all apps at once; the namespace votes
// by app. While the namespace is overloaded (most of its apps slowed
// together, which points at something they share) or holds a limit, the
// actor type estimators stand down and the namespace gate, divided by app
// niceness, takes the load off.
func (c *connections) evaluate() {
	if !c.adaptive || c.now.Before(c.nextEval) {
		return
	}
	c.nextEval = c.now.Add(minEvalPeriod)

	var nsChangedAt time.Time
	if c.nsGate != nil {
		nsChangedAt = c.nsGate.est.changedAt
	}

	// Each app votes once, with the slower of its workflow and activity
	// ratios, so one app cannot make the namespace look shared.
	appRatios := make(map[string]float64)
	var maxPeriod time.Duration
	var typeGates uint32
	var serviceSum float64
	var serviceAcks uint32
	for key, gate := range c.concurrencyGates {
		est := gate.est
		if est == nil {
			continue
		}
		typeGates++
		maxPeriod = max(maxPeriod, est.period)
		est.ratio, est.voters = est.latencyRatio(latest(est.changedAt, nsChangedAt))
		if est.voters > 0 {
			app := c.appKey(key)
			appRatios[app] = max(appRatios[app], est.ratio)
		}
		if est.serviceTime = est.uncongested(); est.serviceTime > 0 {
			serviceSum += float64(est.acks) * float64(est.serviceTime)
			serviceAcks += est.acks
		}
		monitoring.RecordConcurrencyEstimate(key, est.ratio, est.serviceTime)
	}
	var nsServiceTime time.Duration
	if serviceAcks > 0 {
		nsServiceTime = time.Duration(serviceSum / float64(serviceAcks))
	}

	ratios := make([]float64, 0, len(appRatios))
	for _, ratio := range appRatios {
		ratios = append(ratios, ratio)
	}
	var nsRatio float64
	if len(ratios) > 0 {
		nsRatio = median(ratios)
	}
	if c.nsGate != nil {
		monitoring.RecordConcurrencyEstimate(c.nsKey, nsRatio, nsServiceTime)
	}
	nsOverloaded := c.nsGate != nil && len(ratios) >= c.nsGate.est.minVoters && nsRatio >= overloadRatio
	// Under a namespace limit an app's throughput is its share of what the
	// apps share, not its own capacity, so a limit derived from it would
	// freeze the current split in place of niceness.
	nsLimited := c.nsGate != nil && c.nsGate.est.limit > 0

	changed := false
	for key, gate := range c.concurrencyGates {
		est := gate.est
		if est == nil || !est.due(c.now) {
			continue
		}
		ratio := est.ratio
		if nsOverloaded || nsLimited {
			ratio = 0
		}
		ch, overload := est.control(evalInput{
			now:      c.now,
			ratio:    ratio,
			voters:   est.voters,
			inflight: gate.current,
			//nolint:gosec // a host count is never negative
			floor:       uint32(c.streamPool.Hosts(key)),
			ceiling:     gate.ceiling(c.schedulerCount, c.schedulerIdx),
			serviceTime: est.serviceTime,
		})
		if rtt, ok := est.roll(); ok {
			est.period = evalPeriod(rtt)
		}
		c.recordEstimate(key, gate, ch, overload)
		changed = changed || ch
	}

	if ns := c.nsGate; ns != nil && ns.est.due(c.now) {
		ch, overload := ns.est.control(evalInput{
			now:         c.now,
			ratio:       nsRatio,
			voters:      len(ratios),
			inflight:    ns.current,
			floor:       typeGates,
			serviceTime: nsServiceTime,
		})
		ns.est.period = min(max(maxPeriod, minEvalPeriod), maxEvalPeriod)
		c.recordEstimate(c.nsKey, ns, ch, overload)
		changed = changed || ch

		// An app that noticed the shared slowdown a pass before the namespace
		// did may hold an adaptive limit of its own. Left in place, those
		// limits rather than niceness would decide each app's share, so the
		// namespace takes over.
		if overload {
			for key, gate := range c.concurrencyGates {
				if gate.est != nil && gate.est.limit > 0 {
					gate.est.clear(c.now)
					c.recordEstimate(key, gate, true, false)
					changed = true
				}
			}
		}
	}

	if changed {
		c.drain()
	}
}

func (c *connections) recordEstimate(key string, gate *concurrencyGate, changed, overload bool) {
	if overload {
		monitoring.RecordConcurrencyOverload(key)
	}
	if changed {
		limit := gate.localLimit(c.schedulerCount, c.schedulerIdx)
		log.Debugf("Adaptive concurrency limit for %s is now %d (in-flight %d)", key, limit, gate.current)
		monitoring.RecordConcurrencyLimit(key, int64(limit))
	}
}

func (c *connections) recordLimits() {
	for key, gate := range c.concurrencyGates {
		monitoring.RecordConcurrencyLimit(key, int64(gate.localLimit(c.schedulerCount, c.schedulerIdx)))
	}
	if c.nsGate != nil {
		monitoring.RecordConcurrencyLimit(c.nsKey, int64(c.nsGate.localLimit(c.schedulerCount, c.schedulerIdx)))
	}
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
	cancel()

	c.unregisterStream(closeStream.StreamIDx)
	c.drain()

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

	c.failPending(func(string) bool { return false }, api.TriggerResponseResult_UNDELIVERABLE)
	clear(c.concurrencyGates)
	clear(c.streamGateKeys)
	clear(c.niceness)
	clear(c.streamNicenessKeys)
	clear(c.hostLimits)
	clear(c.streamHosts)

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
