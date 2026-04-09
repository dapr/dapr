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
		}
	}}
)

type Options struct {
	Cron           api.Interface
	NamespaceLoop  loop.Interface[loops.EventNS]
	SchedulerCount func() int32
}

// connections is a control loop that creates and manages stream connections,
// piping trigger requests.
type connections struct {
	cron           api.Interface
	nsLoop         loop.Interface[loops.EventNS]
	loop           loop.Interface[loops.EventConn]
	schedulerCount func() int32

	streams          map[uint64]context.CancelFunc
	streamIDx        uint64
	streamPool       *store.Store
	concurrencyGates map[string]*concurrencyGate
	wg               sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventConn] {
	conns := connsCache.Get().(*connections)

	conns.cron = opts.Cron
	conns.nsLoop = opts.NamespaceLoop
	conns.schedulerCount = opts.SchedulerCount
	conns.streamIDx = 0

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
	case *loops.Shutdown:
		c.handleShutdown()
	default:
		return fmt.Errorf("unknown connections event type: %T", e)
	}

	return nil
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

	c.updateConcurrencyLimits(add.Request)

	return nil
}

func (c *connections) updateConcurrencyLimits(req *schedulerv1pb.WatchJobsRequestInitial) {
	var scount uint32 = 1
	if c.schedulerCount != nil {
		//nolint:gosec // scheduler count is always positive
		scount = max(uint32(c.schedulerCount()), 1)
	}

	for _, limit := range req.GetConcurrencyLimits() {
		if limit.GetMaxConcurrent() <= 0 {
			continue
		}
		key := limit.GetGroup()
		if limit.Name != nil {
			key += ":" + limit.GetName()
		}
		//nolint:gosec // guarded by <= 0 check above
		c.upsertGate(key, uint32(limit.GetMaxConcurrent()), scount)
	}
}

func (c *connections) upsertGate(key string, globalLimit, schedulerCount uint32) {
	if gate, ok := c.concurrencyGates[key]; ok {
		gate.globalLimit = globalLimit
		gate.recalculateLocal(schedulerCount)
	} else {
		c.concurrencyGates[key] = newConcurrencyGate(globalLimit, schedulerCount)
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

	var acquired []string
	for _, key := range gateKeys {
		gate := c.concurrencyGates[key]
		if gate.tryAcquire() {
			acquired = append(acquired, key)
		} else {
			for _, akey := range acquired {
				c.concurrencyGates[akey].release()
			}
			monitoring.RecordConcurrencyThrottled(key)
			primaryGate := c.concurrencyGates[gateKeys[0]]
			if !primaryGate.enqueue(req) {
				req.ResultFn(api.TriggerResponseResult_FAILED)
			} else {
				monitoring.RecordConcurrencyPending(gateKeys[0], int64(primaryGate.pendingLen()))
			}
			return
		}
	}

	c.dispatchWithGates(streamLoop, req, gateKeys)
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

// drainPending attempts to dispatch the next pending trigger from a gate.
func (c *connections) drainPending(key string, gate *concurrencyGate) {
	next := gate.dequeue()
	if next == nil {
		return
	}
	monitoring.RecordConcurrencyPending(key, int64(gate.pendingLen()))

	streamLoop, ok := c.getStreamLoop(next.Job.GetMetadata())
	if !ok {
		next.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		c.drainPending(key, gate)
		return
	}

	gateKeys := c.gateKeysForTrigger(next)
	var acquired []string
	for _, gk := range gateKeys {
		g := c.concurrencyGates[gk]
		if g.tryAcquire() {
			acquired = append(acquired, gk)
		} else {
			for _, akey := range acquired {
				c.concurrencyGates[akey].release()
			}
			primaryGate := c.concurrencyGates[gateKeys[0]]
			if !primaryGate.enqueue(next) {
				next.ResultFn(api.TriggerResponseResult_FAILED)
			}
			return
		}
	}

	c.dispatchWithGates(streamLoop, next, gateKeys)
}

// handleCloseStream handles a close stream request.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	cancel, ok := c.streams[closeStream.StreamIDx]
	if !ok {
		return errors.New("catastrophic state machine error: lost connection stream reference")
	}

	delete(c.streams, closeStream.StreamIDx)
	cancel()

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
