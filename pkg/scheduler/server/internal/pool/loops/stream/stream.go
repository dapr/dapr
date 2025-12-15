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

package stream

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.stream")

var (
	streamLoopFactory = loop.New[loops.Event](1024)
	streamCache       = sync.Pool{New: func() any {
		return new(stream)
	}}
)

type Options struct {
	IDx           uint64
	Add           *loops.ConnAdd
	Cron          api.Interface
	NamespaceLoop loop.Interface[loops.Event]
}

// stream is a loop that handles a single stream of a connection. It handles
// triggering and responding to triggers.
type stream struct {
	idx     uint64
	channel schedulerv1pb.Scheduler_WatchJobsServer
	nsLoop  loop.Interface[loops.Event]
	appID   string
	ns      string
	loop    loop.Interface[loops.Event]
	cancel  context.CancelCauseFunc

	// triggerIDx is the uuid of a triggered job. We can use a simple counter as
	// there are no privacy/time attack concerns as this counter is scoped to a
	// single client.
	triggerIDx uint64

	inflight sync.Map // map[uint64]func(api.TriggerResponseResult)

	wg sync.WaitGroup
}

func New(ctx context.Context, opts Options) (loop.Interface[loops.Event], error) {
	cancel, err := handleAdd(ctx, opts.Cron, opts.Add.Request)
	if err != nil {
		return nil, err
	}

	stream := streamCache.Get().(*stream)
	stream.idx = opts.IDx
	stream.nsLoop = opts.NamespaceLoop
	stream.channel = opts.Add.Channel
	stream.ns = opts.Add.Request.GetNamespace()
	stream.appID = opts.Add.Request.GetAppId()
	stream.triggerIDx = 0
	stream.cancel = func(err error) {
		opts.Add.Cancel(err)
		cancel(err)
	}

	stream.loop = streamLoopFactory.NewLoop(stream)

	stream.wg.Add(1)
	go func() {
		stream.recvLoop()
		log.Debugf("Closed receive stream to %s/%s", stream.ns, stream.appID)
		stream.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: stream.idx,
			Namespace: stream.ns,
		})
		stream.wg.Done()
	}()

	return stream.loop, nil
}

func (s *stream) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.TriggerRequest:
		s.handleTriggerRequest(e)
	case *loops.StreamShutdown:
		s.handleShutdown()
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	return nil
}

// handleTriggerRequest handles a trigger request from the scheduler. It sends
// the job to the stream and stores the result function in the inflight map.
func (s *stream) handleTriggerRequest(req *loops.TriggerRequest) {
	s.triggerIDx++
	s.inflight.Store(s.triggerIDx, req.ResultFn)

	job := &schedulerv1pb.WatchJobsResponse{
		Name:     req.Job.GetName(),
		Id:       s.triggerIDx,
		Data:     req.Job.GetData(),
		Metadata: req.Job.GetMetadata(),
	}

	if err := s.channel.Send(job); err != nil {
		log.Warnf("Error sending job to stream %s/%s: %s", s.ns, s.appID, err)
		s.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: s.idx,
			Namespace: s.ns,
		})
	}
}

var errStreamShutdown = errors.New("stream shutdown")

// handleShutdown handles a shutdown request from the scheduler. It closes
// the stream and calls all inflight result functions with an undeliverable
// result.
func (s *stream) handleShutdown() {
	log.Infof("Closing connection to %s/%s", s.ns, s.appID)
	s.cancel(errStreamShutdown)
	s.wg.Wait()
	s.inflight.Range(func(_, fn any) bool {
		fn.(func(api.TriggerResponseResult))(api.TriggerResponseResult_UNDELIVERABLE)
		return true
	})
	s.inflight.Clear()
	streamCache.Put(s)
	streamLoopFactory.CacheLoop(s.loop)
}
