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
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/stream"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

var (
	loopFactory = loop.New[loops.Event](1024)
	connsCache  = sync.Pool{New: func() any {
		return &connections{
			streams:    make(map[uint64]context.CancelFunc),
			streamPool: store.New(),
		}
	}}
)

type Options struct {
	Cron          api.Interface
	NamespaceLoop loop.Interface[loops.Event]
}

// connections is a control loop that creates and manages stream connections,
// piping trigger requests.
// TODO: @joshvanl: use a sync.Pool cache
type connections struct {
	cron   api.Interface
	nsLoop loop.Interface[loops.Event]
	loop   loop.Interface[loops.Event]

	streams    map[uint64]context.CancelFunc
	streamIDx  uint64
	streamPool *store.Store
	wg         sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.Event] {
	conns := connsCache.Get().(*connections)

	conns.cron = opts.Cron
	conns.nsLoop = opts.NamespaceLoop
	conns.streamIDx = 0

	conns.loop = loopFactory.NewLoop(conns)
	return conns.loop
}

func (c *connections) Handle(ctx context.Context, event loops.Event) error {
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

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		_ = streamLoop.Run(ctx)
	}()

	var appID *string
	ts := add.Request.GetAcceptJobTypes()
	if len(ts) == 0 || slices.Contains(add.Request.GetAcceptJobTypes(), schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB) {
		appID = ptr.Of(add.Request.GetAppId())
	}

	c.streams[streamIDx] = c.streamPool.Add(store.Options{
		Loop:       streamLoop,
		AppID:      appID,
		ActorTypes: add.Request.GetActorTypes(),
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

	loopFactory.CacheLoop(c.loop)
}

// getStreamLoop returns a stream loop from the pool based on the metadata.
func (c *connections) getStreamLoop(meta *schedulerv1pb.JobMetadata) (loop.Interface[loops.Event], bool) {
	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		return c.streamPool.AppID(meta.GetAppId())
	case *schedulerv1pb.JobTargetMetadata_Actor:
		return c.streamPool.ActorType(t.GetActor().GetType())
	default:
		return nil, false
	}
}
