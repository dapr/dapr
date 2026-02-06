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

package stream

import (
	"context"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.loops.stream")

var (
	LoopFactory = loop.New[loops.EventStream](8)
	streamCache = sync.Pool{New: func() any {
		return new(stream)
	}}
)

type Options struct {
	Channel       v1pb.Placement_ReportDaprStatusClient
	PlacementLoop loop.Interface[loops.EventPlace]
	IDx           uint64
}

type stream struct {
	channel   v1pb.Placement_ReportDaprStatusClient
	placeLoop loop.Interface[loops.EventPlace]
	idx       uint64

	loop loop.Interface[loops.EventStream]

	wg sync.WaitGroup
}

func New(ctx context.Context, opts Options) loop.Interface[loops.EventStream] {
	stream := streamCache.Get().(*stream)
	stream.channel = opts.Channel
	stream.placeLoop = opts.PlacementLoop
	stream.idx = opts.IDx

	stream.loop = LoopFactory.NewLoop(stream)

	stream.wg.Add(1)
	go func() {
		defer stream.wg.Done()
		err := stream.recvLoop()
		stream.placeLoop.Enqueue(&loops.ConnCloseStream{
			Error: err,
			IDx:   stream.idx,
		})
	}()

	return stream.loop
}

func (s *stream) Handle(ctx context.Context, event loops.EventStream) error {
	var err error
	switch e := event.(type) {
	case *loops.StreamSend:
		err = s.handleSend(e)
	case *loops.Shutdown:
		s.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	if err != nil {
		log.Errorf("Error handling stream event %T: %v", event, err)
		s.placeLoop.Enqueue(&loops.ConnCloseStream{
			Error: err,
			IDx:   s.idx,
		})
	}

	return nil
}

func (s *stream) handleSend(e *loops.StreamSend) error {
	return s.channel.Send(e.Host)
}

func (s *stream) handleShutdown(e *loops.Shutdown) {
	log.Infof("Closing connection to placement: %s", e.Error)
	s.channel.CloseSend()
	s.wg.Wait()
	streamCache.Put(s)
}
