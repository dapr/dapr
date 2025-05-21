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
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/stream"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/store"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.connections")

// TODO: @joshvanl: Use a universal `loops.Event` cache for all loops packages.
//var (
//	streamLoopCache = sync.Pool{New: func() any {
//		return loop.Empty[loops.Event]()
//	}}
//)

type Options struct {
	Cron       api.Interface
	CancelPool context.CancelCauseFunc
}

// TODO: @joshvanl
type connections struct {
	cron       api.Interface
	cancelPool context.CancelCauseFunc
	loop       loop.Interface[loops.Event]

	streams    map[uint64]context.CancelCauseFunc
	streamIDx  uint64
	streamPool *store.Namespace
	wg         sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.Event] {
	// TODO: @joshvanl: cache.
	conns := &connections{
		streams:    make(map[uint64]context.CancelCauseFunc),
		cancelPool: opts.CancelPool,
		cron:       opts.Cron,
		streamPool: store.New(),
	}

	//loop := jobLoopCache.Get().(loop.Interface[loops.Event])
	loop := loop.Empty[loops.Event]().Reset(conns, 1024)
	conns.loop = loop
	return loop
}

func (c *connections) Handle(ctx context.Context, event loops.Event) error {
	fmt.Printf(">>Handling Connection event: %T %v\n", event, event)

	switch e := event.(type) {
	case *loops.ConnAdd:
		return c.handleAdd(ctx, e)
	case *loops.ConnCloseStream:
		c.handleCloseStream(e)
	case *loops.TriggerRequest:
		c.handleTriggerRequest(e)
	case *loops.Shutdown:
		c.handleShutdown()
	default:
		return fmt.Errorf("unknown connections event type: %T", e)
	}

	return nil
}

// handleAdd adds a connection to the pool for a given namespace/appID.
func (c *connections) handleAdd(ctx context.Context, add *loops.ConnAdd) error {
	var prefixes []string
	var appID *string

	fmt.Printf(">>HANDLING ADD: %v\n", add)

	reqNamespace := add.Request.GetNamespace()
	reqAppID := add.Request.GetAppId()

	// To account for backwards compatibility where older clients did not use
	// this field, we assume a connected client and implement both app jobs, as
	// well as actor job types. We can remove this in v1.16
	ts := add.Request.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		log.Infof("Adding a Sidecar connection to Scheduler for appID: %s/%s.", reqNamespace, reqAppID)
		appID = &add.Request.AppId
		prefixes = append(prefixes, "app||"+reqNamespace+"||"+reqAppID+"||")
	}

	if len(ts) == 0 || slices.Contains(ts, schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER) {
		for _, actorType := range add.Request.GetActorTypes() {
			log.Infof("Adding a Sidecar connection to Scheduler for actor type: %s/%s.", reqNamespace, actorType)
			prefixes = append(prefixes, "actorreminder||"+reqNamespace+"||"+actorType+"||")
		}
	}

	log.Debugf("Marking deliverable prefixes for Sidecar connection: %s/%s: %v.",
		add.Request.GetNamespace(), add.Request.GetAppId(), prefixes)

	fmt.Printf(">>%v\n", appID)

	pcancel, err := c.cron.DeliverablePrefixes(ctx, prefixes...)
	if err != nil {
		return err
	}

	log.Debugf("Added a Sidecar connection to Scheduler for: %s/%s.",
		add.Request.GetNamespace(), add.Request.GetAppId())

	streamIDx := c.streamIDx
	c.streamIDx++

	streamLoop := stream.New(stream.Options{
		IDx:      streamIDx,
		Channel:  add.Channel,
		Request:  add.Request,
		ConnLoop: c.loop,
	})

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		err := streamLoop.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Errorf("Error running stream loop: %v", err)
			c.cancelPool(err)
		}
	}()

	c.streams[streamIDx] = c.streamPool.Add(store.Options{
		Namespace:  add.Request.Namespace,
		AppID:      appID,
		ActorTypes: add.Request.ActorTypes,
		Connection: &store.StreamConnection{
			Cancel: func(err error) {
				pcancel()
				add.Cancel(err)
			},
			Loop: streamLoop,
		},
	})

	return nil
}

// handleTriggerRequest handles a trigger request for a job.
func (c *connections) handleTriggerRequest(req *loops.TriggerRequest) {
	loop, ok := c.getStreamLoop(req.Job.GetMetadata())
	if !ok {
		req.ResultFn(api.TriggerResponseResult_UNDELIVERABLE)
		return
	}

	loop.Enqueue(req)
}

// handleCloseStream handles a close stream request.
func (c *connections) handleCloseStream(closeStream *loops.ConnCloseStream) error {
	cancel, ok := c.streams[closeStream.StreamIDx]
	if !ok {
		return errors.New("catastrophic state machine error: lost connection stream reference")
	}

	cancel(nil)
	delete(c.streams, closeStream.StreamIDx)
	return nil
}

// handleShutdown handles the shutdown of the connections.
func (c *connections) handleShutdown() {
	defer c.wg.Wait()

	for _, cancel := range c.streams {
		cancel(nil)
	}

	c.streams = make(map[uint64]context.CancelCauseFunc)
}

// getStreamLoop returns a stream loop from the pool based on the metadata.
func (c *connections) getStreamLoop(meta *schedulerv1pb.JobMetadata) (loop.Interface[loops.Event], bool) {
	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return c.streamPool.AppID(meta.GetNamespace(), meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		return c.streamPool.ActorType(meta.GetNamespace(), t.GetActor().GetType())
	default:
		return nil, false
	}
}
