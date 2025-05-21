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
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
	"github.com/diagridio/go-etcd-cron/api"
)

var log = logger.NewLogger("dapr.scheduler.server.pool.loops.stream")

// TODO: @joshvanl: Use a universal `loops.Event` cache for all loops packages.
//var (
//	streamLoopCache = sync.Pool{New: func() any {
//		return loop.Empty[loops.Event]()
//	}}
//)

type Options struct {
	IDx      uint64
	Channel  schedulerv1pb.Scheduler_WatchJobsServer
	Request  *schedulerv1pb.WatchJobsRequestInitial
	ConnLoop loop.Interface[loops.Event]
}

// stream is a loop that handles a single stream of a connection. It handles
// triggering and responding to triggers.
type stream struct {
	idx      uint64
	channel  schedulerv1pb.Scheduler_WatchJobsServer
	connLoop loop.Interface[loops.Event]
	ns       string
	appID    string

	// triggerIDx is the uuid of a triggered job. We can use a simple counter as
	// there are no privacy/time attack concerns as this counter is scoped to a
	// single client.
	triggerIDx uint64

	inflight map[uint64]func(api.TriggerResponseResult)
	lock     sync.Mutex

	doneCh chan struct{}
}

func New(opts Options) loop.Interface[loops.Event] {
	// TODO: @joshvanl: cache.
	stream := &stream{
		idx:      opts.IDx,
		channel:  opts.Channel,
		connLoop: opts.ConnLoop,
		inflight: make(map[uint64]func(api.TriggerResponseResult)),
		ns:       opts.Request.GetNamespace(),
		appID:    opts.Request.GetAppId(),
		doneCh:   make(chan struct{}),
	}

	//loop := jobLoopCache.Get().(loop.Interface[loops.Event])
	loop := loop.Empty[loops.Event]().Reset(stream, 5)

	go stream.recvLoop()

	return loop
}

func (s *stream) Handle(ctx context.Context, event loops.Event) error {
	fmt.Printf(">>Handling Stream event: %T %v\n", event, event)

	switch e := event.(type) {
	case *loops.TriggerRequest:
		s.handleTriggerRequest(e)
	case *loops.StreamShutdown:
		s.handleShutdown()
	default:
		return fmt.Errorf("unknown stream event type: %T", e)
	}

	return nil
}

// handleTriggerRequest handles a trigger request from the scheduler. It sends
// the job to the stream and stores the result function in the inflight map.
func (s *stream) handleTriggerRequest(req *loops.TriggerRequest) {
	s.triggerIDx++
	s.lock.Lock()
	s.inflight[s.triggerIDx] = req.ResultFn
	s.lock.Unlock()

	job := &schedulerv1pb.WatchJobsResponse{
		Name:     req.Job.GetName(),
		Id:       s.triggerIDx,
		Data:     req.Job.GetData(),
		Metadata: req.Job.GetMetadata(),
	}

	fmt.Printf(">>SENDING JOB: %v\n", job)
	if err := s.channel.Send(job); err != nil {
		log.Warnf("Error sending job to stream %s/%s: %s", s.ns, s.appID, err)
		fmt.Printf("<<<HERE1123\n")
		s.connLoop.Enqueue(&loops.ConnCloseStream{StreamIDx: s.idx})
	}
}

// handleShutdown handles a shutdown request from the scheduler. It closes
// the stream and calls all inflight result functions with an undeliverable
// result.
func (s *stream) handleShutdown() {
	<-s.doneCh
	for _, fn := range s.inflight {
		fn(api.TriggerResponseResult_UNDELIVERABLE)
	}
}

// recvLoop is the main loop for receiving messages from the stream. It
// handles errors and calls the recv function to receive messages.
func (s *stream) recvLoop() {
	defer func() {
		log.Debugf("Closed receive stream to %s/%s", s.ns, s.appID)
		s.connLoop.Enqueue(&loops.ConnCloseStream{StreamIDx: s.idx})
		close(s.doneCh)
	}()

	for {
		err := s.recv()
		if err == nil {
			continue
		}

		isEOF := errors.Is(err, io.EOF)
		status, ok := status.FromError(err)
		if s.channel.Context().Err() != nil || isEOF || (ok && status.Code() != codes.Canceled) {
			return
		}

		log.Warnf("Error receiving from stream %s/%s: %s", s.ns, s.appID, err)
		return
	}
}

// recv receives a message from the stream. It checks the result and calls
// the inflight result function with the appropriate result.
func (s *stream) recv() error {
	resp, err := s.channel.Recv()
	fmt.Printf(">>GOT RECV: %v %v\n", resp, err)
	if err != nil {
		return err
	}

	result := resp.GetResult()
	if result == nil {
		return errors.New("received nil result from stream")
	}

	s.lock.Lock()
	inf, ok := s.inflight[result.GetId()]
	if !ok {
		s.lock.Unlock()
		return errors.New("received unknown trigger response from stream")
	}

	delete(s.inflight, result.GetId())
	s.lock.Unlock()

	switch result.GetStatus() {
	case schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS:
		inf(api.TriggerResponseResult_SUCCESS)
	case schedulerv1pb.WatchJobsRequestResultStatus_FAILED:
		inf(api.TriggerResponseResult_FAILED)
	default:
		return fmt.Errorf("unknown trigger response status: %s", result.GetStatus())
	}

	return nil
}
