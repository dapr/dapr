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
	LoopFactory = loop.New[loops.EventDisseminator](1024)
	dissCache   = sync.Pool{New: func() any {
		return &disseminator{
			streams: make(map[uint64]*streamConn),
		}
	}}
)

type Options struct {
	NamespaceLoop        loop.Interface[loops.EventNamespace]
	Namespace            string
	ReplicationFactor    int64
	Authorizer           *authorizer.Authorizer
	DisseminationTimeout time.Duration
}

type streamConn struct {
	loop         loop.Interface[loops.EventStream]
	currentState v1pb.HostOperation
	hasActors    bool

	// receivingTable is set to the version of a one-shot table push sent to this
	// stream. Responses from the client for this version are ignored by the
	// disseminator since they are not part of the namespace-wide dissemination
	// protocol. It is set to nil when no table push is in progress.
	receivingTable *uint64
}

// disseminator is a control loop that creates and manages stream connections,
// disseminating actor type updates with a 3 stage lock.
type disseminator struct {
	nsLoop     loop.Interface[loops.EventNamespace]
	loop       loop.Interface[loops.EventDisseminator]
	authorizer *authorizer.Authorizer
	timeout    time.Duration

	namespace string

	timeoutQ *timeout.Timeout

	streams              map[uint64]*streamConn
	streamsInTargetState int

	store     *store.Store
	streamIDx uint64
	wg        sync.WaitGroup

	currentOperation     v1pb.HostOperation
	currentVersion       uint64
	connCount            atomic.Int64
	actorConnCount       atomic.Int64
	waitingToDisseminate []*loops.ConnAdd
	waitingToDelete      []uint64
}

func New(opts Options) loop.Interface[loops.EventDisseminator] {
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
	diss.streamsInTargetState = 0

	diss.waitingToDisseminate = nil
	diss.waitingToDelete = nil

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

func (d *disseminator) Handle(ctx context.Context, event loops.EventDisseminator) error {
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

// addStream creates a stream loop for the connection and registers it in the
// streams map.
func (d *disseminator) addStream(ctx context.Context, add *loops.ConnAdd) uint64 {
	streamIDx := d.streamIDx
	d.streamIDx++

	streamLoop := stream.New(ctx, stream.Options{
		IDx:           streamIDx,
		Add:           add,
		NamespaceLoop: d.nsLoop,
		Authorizer:    d.authorizer,
	})

	d.wg.Go(func() {
		derr := streamLoop.Run(ctx)
		if derr != nil {
			log.Errorf("Stream loop for stream %s:%d exited with error: %v", d.namespace, streamIDx, derr)
		}
	})

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

	return streamIDx
}

// handleAdd adds a stream to the namespaced disseminator.
func (d *disseminator) handleAdd(ctx context.Context, add *loops.ConnAdd) {
	// If we are currently disseminating a lock, queue this addition.
	if d.currentOperation != v1pb.HostOperation_REPORT {
		d.waitingToDisseminate = append(d.waitingToDisseminate, add)
		return
	}

	// Process any queued deletions before adding the new stream. During a
	// rolling update, disconnects (queued as waitingToDelete) and new
	// connections arrive in rapid succession. Processing deletions here lets the
	// subsequent doReport include both the removal and addition in a single
	// dissemination round instead of alternating delete-round → add-round
	// cycles.
	for _, toDelete := range d.waitingToDelete {
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

	// Also clean up orphaned store entries,  store entries whose streams have
	// already been removed from d.streams but whose ConnCloseStream event hasn't
	// arrived at the disseminator loop yet. This happens during rolling updates
	// where CloseSend + new connection arrive faster than the close event
	// propagates through the stream -> namespace -> disseminator queues.
	d.store.CollectOrphans(func(idx uint64) bool {
		_, ok := d.streams[idx]
		return ok
	}, &d.waitingToDelete)
	for _, toDelete := range d.waitingToDelete {
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

	streamIDx := d.addStream(ctx, add)
	d.handleReportedHost(ctx, &loops.ReportedHost{
		Host:      add.InitialHost,
		StreamIDx: streamIDx,
	})
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
	d.waitingToDisseminate = nil
	d.waitingToDelete = nil
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

	// Only close streams that have NOT reached the current target state. Streams
	// that responded successfully to the current dissemination phase are healthy
	// and should not be punished for another stream's slowness.
	for idx, s := range d.streams {
		if s.currentState == d.currentOperation {
			// This stream already responded to the current phase, it's healthy.
			continue
		}

		log.Warnf("Closing non-responding stream %s:%d (state=%s, expected=%s)",
			d.namespace, idx, s.currentState.String(), d.currentOperation.String())

		d.store.Delete(idx)
		monitoring.RecordRuntimesCount(d.connCount.Add(-1), d.namespace)
		if s.hasActors {
			monitoring.RecordActorRuntimesCount(d.actorConnCount.Add(-1), d.namespace)
		}
		s.loop.Close(&loops.StreamShutdown{
			Error: err,
		})
		stream.StreamLoopFactory.CacheLoop(s.loop)
		delete(d.streams, idx)
	}

	// Reset dissemination state. Process any deletions that were queued during
	// the round (from streams that disconnected while we were disseminating).
	// These must be applied to the store before starting the next round,
	// otherwise the deleted hosts remain in the table.
	for _, toDelete := range d.waitingToDelete {
		d.store.Delete(toDelete)
	}
	d.waitingToDelete = nil

	d.currentVersion++
	d.currentOperation = v1pb.HostOperation_REPORT
	d.streamsInTargetState = 0

	// Add any waiting connections- they should not be punished for another
	// stream's slowness. They are added regardless of whether surviving streams
	// exist, because the waiting connections themselves become the new stream
	// set.
	if len(d.waitingToDisseminate) > 0 {
		waiting := d.waitingToDisseminate
		d.waitingToDisseminate = nil

		for _, add := range waiting {
			streamIDx := d.addStream(ctx, add)
			d.store.Set(streamIDx, add.InitialHost)
		}
	}

	if len(d.streams) > 0 {
		// Start a new dissemination round with all remaining + newly added
		// streams.
		d.timeoutQ.Enqueue(d.currentVersion)
		d.currentOperation = v1pb.HostOperation_LOCK
		for _, s := range d.streams {
			s.currentState = v1pb.HostOperation_REPORT
			s.receivingTable = nil
			s.loop.Enqueue(&loops.DisseminateLock{
				Version: d.currentVersion,
			})
		}
	} else {
		monitoring.RecordRuntimesCount(0, d.namespace)
		monitoring.RecordActorRuntimesCount(0, d.namespace)
	}
}
