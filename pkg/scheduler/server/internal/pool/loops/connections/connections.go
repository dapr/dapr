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
	"errors"
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
)

var (
	loopFactory = loop.New[loops.EventConn](1024)
	connsCache  = sync.Pool{New: func() any {
		return &connections{
			streams:          make(map[uint64]context.CancelFunc),
			streamPool:       store.New(),
			concurrencyGates: make(map[string]*concurrencyGate),
			streamGateKeys:   make(map[uint64][]string),
		}
	}}
)

type Options struct {
	Cron          api.Interface
	NamespaceLoop loop.Interface[loops.EventNS]
}

// connections is a control loop that creates and manages stream connections,
// piping trigger requests.
type connections struct {
	cron   api.Interface
	nsLoop loop.Interface[loops.EventNS]
	loop   loop.Interface[loops.EventConn]

	// schedulerCount and schedulerIdx are the latest view of cluster
	// membership, updated by SchedulerInfoUpdate events. Only accessed from
	// the connections loop goroutine, so plain fields are race-free.
	// Defaults to {count: 1, idx: 0} until the first event arrives.
	schedulerCount uint32
	schedulerIdx   uint32

	streams          map[uint64]context.CancelFunc
	streamIDx        uint64
	streamPool       *store.Store
	concurrencyGates map[string]*concurrencyGate
	streamGateKeys   map[uint64][]string
	wg               sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventConn] {
	conns := connsCache.Get().(*connections)

	conns.cron = opts.Cron
	conns.nsLoop = opts.NamespaceLoop
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
		Loop:       streamLoop,
		AppID:      appID,
		ActorTypes: add.Request.GetActorTypes(),
	})

	c.updateConcurrencyLimits(streamIDx, add.Request)

	return nil
}

func (c *connections) updateConcurrencyLimits(streamIDx uint64, req *schedulerv1pb.WatchJobsRequestInitial) {
	var keys []string
	for _, limit := range req.GetConcurrencyLimits() {
		if limit.GetMaxConcurrent() <= 0 {
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
}

// drainPending scans the pending queue to find a trigger that can acquire all
// required gates. This avoids head-of-line blocking when the first pending
// trigger is blocked on a different gate than the one that just released.
func (c *connections) drainPending(key string, gate *concurrencyGate) {
	defer monitoring.RecordConcurrencyPending(key, int64(gate.pendingLen()))

	n := gate.pendingLen()
	for range n {
		next := gate.dequeue()
		if next == nil {
			return
		}

		if c.tryDispatchPending(next) {
			return
		}
	}
}

// tryDispatchPending attempts to dispatch a single pending trigger. On gate
// acquisition failure it re-queues the request (or fails it if the queue is
// full) and releases any partially-acquired gates via defer. Returns true iff
// the trigger was dispatched.
func (c *connections) tryDispatchPending(next *loops.TriggerRequest) bool {
	streamLoop, ok := c.getStreamLoop(next.Job.GetMetadata())
	if !ok {
		next.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return false
	}

	gateKeys := c.gateKeysForTrigger(next)

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
		primaryGate := c.concurrencyGates[gateKeys[0]]
		if !primaryGate.enqueue(next) {
			next.ResultFn(api.TriggerResponseResult_FAILED)
		}
		return false
	}

	c.dispatchWithGates(streamLoop, next, gateKeys)
	dispatched = true
	return true
}

// handleCloseStream handles a close stream request.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	cancel, ok := c.streams[closeStream.StreamIDx]
	if !ok {
		return errors.New("catastrophic state machine error: lost connection stream reference")
	}

	delete(c.streams, closeStream.StreamIDx)
	delete(c.streamGateKeys, closeStream.StreamIDx)
	cancel()

	c.removeOrphanedGates()

	return nil
}

// handleShutdown handles the shutdown of the connections.
func (c *connections) handleShutdown() {
	defer c.wg.Wait()

	for _, cancel := range c.streams {
		cancel()
	}

	clear(c.streams)

	for _, gate := range c.concurrencyGates {
		for req := gate.dequeue(); req != nil; req = gate.dequeue() {
			req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		}
	}
	clear(c.concurrencyGates)
	clear(c.streamGateKeys)

	loopFactory.CacheLoop(c.loop)
	connsCache.Put(c)
}

// getStreamLoop returns a stream loop from the pool based on the metadata.
func (c *connections) getStreamLoop(meta *schedulerv1pb.JobMetadata) (loop.Interface[loops.EventStream], bool) {
	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return c.streamPool.AppID(meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		return c.streamPool.ActorType(t.GetActor().GetType())
	default:
		return nil, false
	}
}
