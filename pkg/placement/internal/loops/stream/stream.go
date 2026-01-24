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

	"google.golang.org/grpc/peer"

	"github.com/dapr/dapr/pkg/placement/internal/authorizer"
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.server.loops.stream")

var (
	streamLoopFactory = loop.New[loops.Event](8)
	streamCache       = sync.Pool{New: func() any {
		return new(stream)
	}}
)

type Options struct {
	IDx           uint64
	Add           *loops.ConnAdd
	NamespaceLoop loop.Interface[loops.Event]
	Authorizer    *authorizer.Authorizer
}

// stream is a loop that handles a single stream of a connection. It handles
// receiving reported hosts, and sending dissemination operations.
type stream struct {
	idx uint64
	ns  string

	channel v1pb.Placement_ReportDaprStatusServer
	cancel  context.CancelCauseFunc
	nsLoop  loop.Interface[loops.Event]
	authz   *authorizer.Authorizer

	loop loop.Interface[loops.Event]

	currentVersion *uint64

	addr string
	wg   sync.WaitGroup
}

func New(ctx context.Context, opts Options) loop.Interface[loops.Event] {
	addr := "unknown"
	if p, ok := peer.FromContext(opts.Add.Channel.Context()); ok {
		addr = p.Addr.String()
	}

	stream := streamCache.Get().(*stream)
	stream.channel = opts.Add.Channel
	stream.cancel = opts.Add.Cancel
	stream.authz = opts.Authorizer

	stream.loop = streamLoopFactory.NewLoop(stream)
	stream.nsLoop = opts.NamespaceLoop

	stream.ns = opts.Add.InitialHost.GetNamespace()
	stream.idx = opts.IDx

	stream.currentVersion = nil

	stream.addr = addr

	stream.wg.Add(1)
	go func() {
		err := stream.recvLoop()
		stream.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: stream.idx,
			Namespace: stream.ns,
			Error:     err,
		})
		stream.wg.Done()
	}()

	return stream.loop
}

func (s *stream) Handle(ctx context.Context, event loops.Event) error {
	var err error
	switch e := event.(type) {
	case *v1pb.Host:
		s.handleRecive(e)
	case *loops.DisseminateLock:
		err = s.handleLock(e.Version)
	case *loops.DisseminateUpdate:
		err = s.handleUpdate(e.Version, e.Tables)
	case *loops.DisseminateUnlock:
		err = s.handleUnlock(e.Version)
	case *loops.StreamShutdown:
		s.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	if err != nil {
		log.Errorf("Error handling stream event %T on %s: %v", event, s.addr, err)
		s.nsLoop.Enqueue(&loops.ConnCloseStream{
			StreamIDx: s.idx,
			Namespace: s.ns,
		})
	}

	return nil
}

// handleShutdown handles a shutdown request from placement. It closes the
// stream.
func (s *stream) handleShutdown(e *loops.StreamShutdown) {
	log.Infof("Closing connection to %s: %s", s.addr, e.Error)
	s.cancel(e.Error)
	s.wg.Wait()
	streamLoopFactory.CacheLoop(s.loop)
	streamCache.Put(s)
}
