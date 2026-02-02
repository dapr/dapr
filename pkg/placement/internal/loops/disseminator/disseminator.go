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

package disseminator

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/placement/internal/authorizer"
	"github.com/dapr/dapr/pkg/placement/internal/loops"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/store"
	"github.com/dapr/dapr/pkg/placement/internal/loops/disseminator/timeout"
	"github.com/dapr/dapr/pkg/placement/internal/loops/stream"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.server.loops.disseminator")

var (
	LoopFactory = loop.New[loops.Event](1024)
	dissCache   = sync.Pool{New: func() any {
		return &disseminator{
			streams: make(map[uint64]*streamConn),
		}
	}}
)

type Options struct {
	NamespaceLoop        loop.Interface[loops.Event]
	Namespace            string
	ReplicationFactor    int64
	Authorizer           *authorizer.Authorizer
	DisseminationTimeout time.Duration
}

type streamConn struct {
	loop         loop.Interface[loops.Event]
	currentState v1pb.HostOperation
	hasActors    bool
}

// disseminator is a control loop that creates and manages stream connections,
// disseminating actor type updates with a 3 stage lock.
type disseminator struct {
	nsLoop     loop.Interface[loops.Event]
	loop       loop.Interface[loops.Event]
	authorizer *authorizer.Authorizer
	timeout    time.Duration

	namespace string

	timeoutQ *timeout.Timeout

	streams   map[uint64]*streamConn
	store     *store.Store
	streamIDx uint64
	wg        sync.WaitGroup

	currentOperation v1pb.HostOperation
	currentVersion   uint64
	connCount        atomic.Int64
	actorConnCount   atomic.Int64

	waitingToDisseminate []*loops.ConnAdd
}

func New(opts Options) loop.Interface[loops.Event] {
	diss := dissCache.Get().(*disseminator)

	diss.nsLoop = opts.NamespaceLoop
	diss.authorizer = opts.Authorizer
	diss.streamIDx = 0
	diss.currentOperation = v1pb.HostOperation_REPORT
	diss.currentVersion = 0
	diss.connCount.Store(0)
	diss.actorConnCount.Store(0)
	diss.namespace = opts.Namespace
	diss.timeout = opts.DisseminationTimeout

	diss.waitingToDisseminate = diss.waitingToDisseminate[:0]

	if diss.store == nil {
		diss.store = store.New(store.Options{
			ReplicationFactor: opts.ReplicationFactor,
		})
	}

	diss.loop = LoopFactory.NewLoop(diss)

	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    diss.loop,
		Timeout: opts.DisseminationTimeout,
	})

	return diss.loop
}

func (d *disseminator) Handle(ctx context.Context, event loops.Event) error {
	log.Debugf("Disseminator handling event (%s): %T", d.namespace, event)

	switch e := event.(type) {
	case *loops.ConnAdd:
		d.handleAdd(ctx, e)
	case *loops.ReportedHost:
		d.handleReportedHost(ctx, e)
	case *loops.ConnCloseStream:
		d.handleCloseStream(e)
	case *loops.Shutdown:
		d.handleShutdown(e)
	case *loops.DisseminationTimeout:
		d.handleTimeout(ctx, e)
	case *loops.NamespaceTableRequest:
		d.handleTableRequest(e)
	default:
		panic(fmt.Sprintf("unknown disseminator event type: %T", e))
	}

	return nil
}

// handleAdd adds a stream to the namespaced disseminator.
func (d *disseminator) handleAdd(ctx context.Context, add *loops.ConnAdd) {
	// If we are currently disseminating a lock, queue this addition.
	if d.currentOperation != v1pb.HostOperation_REPORT {
		d.waitingToDisseminate = append(d.waitingToDisseminate, add)
		return
	}

	streamIDx := d.streamIDx
	d.streamIDx++

	streamLoop := stream.New(ctx, stream.Options{
		IDx:           streamIDx,
		Add:           add,
		NamespaceLoop: d.nsLoop,
		Authorizer:    d.authorizer,
	})

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		_ = streamLoop.Run(ctx)
	}()

	stream := &streamConn{
		loop:         streamLoop,
		currentState: v1pb.HostOperation_REPORT,
		hasActors:    len(add.InitialHost.GetEntities()) > 0,
	}

	monitoring.RecordRuntimesCount(d.connCount.Add(1), add.InitialHost.GetNamespace())
	if stream.hasActors {
		monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(1), add.InitialHost.GetNamespace())
	}

	d.streams[streamIDx] = stream
	d.handleReportedHost(ctx, &loops.ReportedHost{
		Host:      add.InitialHost,
		StreamIDx: streamIDx,
	})
}

// handleCloseStream handles a close stream request.
func (d *disseminator) handleCloseStream(closeStream *loops.ConnCloseStream) {
	s, ok := d.streams[closeStream.StreamIDx]
	if !ok {
		// Ignore old streams.
		return
	}

	monitoring.RecordRuntimesCount(d.connCount.Add(-1), d.namespace)
	if s.hasActors {
		monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(-1), d.namespace)
	}

	d.store.Delete(closeStream.StreamIDx)
	delete(d.streams, closeStream.StreamIDx)
	s.loop.Close(&loops.StreamShutdown{
		Error: closeStream.Error,
	})
	stream.StreamLoopFactory.CacheLoop(s.loop)

	d.timeoutQ.Dequeue(d.currentVersion)
	d.currentVersion++
	d.timeoutQ.Enqueue(d.currentVersion)
	d.currentOperation = v1pb.HostOperation_LOCK

	for _, s := range d.streams {
		s.currentState = v1pb.HostOperation_REPORT
		s.loop.Enqueue(&loops.DisseminateLock{
			Version: d.currentVersion,
		})
	}
}

// handleShutdown handles the shutdown of the streams.
func (d *disseminator) handleShutdown(shutdown *loops.Shutdown) {
	defer d.wg.Wait()

	for _, s := range d.streams {
		go func(s *streamConn) {
			s.loop.Close(&loops.StreamShutdown{
				Error: shutdown.Error,
			})

			stream.StreamLoopFactory.CacheLoop(s.loop)
		}(s)
	}

	for _, wait := range d.waitingToDisseminate {
		wait.Cancel(shutdown.Error)
	}

	clear(d.streams)
	d.waitingToDisseminate = d.waitingToDisseminate[:0]
	d.store.DeleteAll()
	d.timeoutQ.Close()

	monitoring.RecordRuntimesCount(0, d.namespace)
	monitoring.RecordActorRuntimesCount(0, d.namespace)

	dissCache.Put(d)
}

func (d *disseminator) handleTimeout(ctx context.Context, timeout *loops.DisseminationTimeout) {
	if timeout.Version != d.currentVersion {
		// Ignore old timeouts.
		return
	}

	err := status.Errorf(
		codes.DeadlineExceeded,
		"dissemination timeout after %s for version %d",
		d.timeout,
		timeout.Version,
	)

	log.Warnf("Dissemination timeout for version %d", timeout.Version)
	for idx, s := range d.streams {
		monitoring.RecordRuntimesCount(d.connCount.Add(-1), d.namespace)
		if s.hasActors {
			monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(-1), d.namespace)
		}

		d.store.Delete(idx)
		s.loop.Close(&loops.StreamShutdown{
			Error: err,
		})
		stream.StreamLoopFactory.CacheLoop(s.loop)
	}

	clear(d.streams)
	d.currentVersion++
	d.currentOperation = v1pb.HostOperation_REPORT

	for _, add := range d.waitingToDisseminate {
		add.Cancel(err)
	}

	if len(d.waitingToDisseminate) == 0 {
		return
	}

	needs := d.waitingToDisseminate[0]
	d.waitingToDisseminate = d.waitingToDisseminate[1:]
	d.handleAdd(ctx, needs)
}
