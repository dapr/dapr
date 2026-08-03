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

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/timeout"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/healthz"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.loops.disseminator")

var (
	LoopFactoryCache = loop.New[loops.EventDiss](1024)
	loopCache        = sync.Pool{New: func() any {
		return new(disseminator)
	}}
)

type Options struct {
	Channel       transport.Transport
	PlacementLoop loop.Interface[loops.EventPlace]
	ActorTable    table.Interface
	Scheduler     schedclient.Reloader
	IDx           uint64
	HTarget       healthz.Target

	DisseminationTimeout time.Duration
	Ready                *atomic.Bool

	Inflight  *inflight.Inflight
	Namespace string
	ID        string

	// V2 speaks the v2 (scheduler placement) protocol: seq-keyed rounds
	// scoped to actor types with partial table merges.
	V2 bool
}

type disseminator struct {
	namespace string
	id        string

	loop         loop.Interface[loops.EventDiss]
	inflight     *inflight.Inflight
	actorTable   table.Interface
	scheduler    schedclient.Reloader
	healthTarget healthz.Target
	ready        *atomic.Bool

	timeout        time.Duration
	timeoutQ       *timeout.Timeout
	timeoutVersion uint64

	streamLoop loop.Interface[loops.EventStream]

	wg sync.WaitGroup

	currentOperation loops.OrderOp
	currentVersion   uint64

	// roundChangedTypes accumulates the union of actor types whose hash
	// ring changed across all UPDATE messages since the last UNLOCK. The
	// placement server may compress multiple rounds (LOCK n, UPDATE n,
	// LOCK n+1, UPDATE n+1, UNLOCK n+1) by eliding intermediate UNLOCKs;
	// in that case, the final UNLOCK must release every type accumulated
	// across the compressed rounds, not just the most recent UPDATE.
	roundChangedTypes map[string]struct{}

	// v2 speaks the v2 (scheduler placement) protocol.
	v2 bool

	// v2Rounds are the in-flight v2 dissemination rounds, keyed by seq.
	v2Rounds map[uint64]*v2Round
}

func New(ctx context.Context, opts Options) loop.Interface[loops.EventDiss] {
	diss := loopCache.Get().(*disseminator)

	diss.namespace = opts.Namespace
	diss.id = opts.ID
	diss.actorTable = opts.ActorTable
	diss.scheduler = opts.Scheduler

	diss.currentOperation = loops.OrderLock
	diss.currentVersion = 0
	diss.timeoutVersion = 0
	diss.roundChangedTypes = make(map[string]struct{})
	diss.v2 = opts.V2
	diss.v2Rounds = make(map[uint64]*v2Round)
	diss.healthTarget = opts.HTarget
	diss.ready = opts.Ready

	diss.loop = LoopFactoryCache.NewLoop(diss)
	diss.inflight = opts.Inflight

	diss.timeout = opts.DisseminationTimeout
	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    diss.loop,
		Timeout: opts.DisseminationTimeout,
	})
	diss.streamLoop = stream.New(ctx, stream.Options{
		Channel:       opts.Channel,
		PlacementLoop: opts.PlacementLoop,
		IDx:           opts.IDx,
	})

	diss.wg.Go(func() {
		derr := diss.streamLoop.Run(ctx)
		if derr != nil {
			log.Errorf("Stream loop ended with error: %s", derr)
		}
	})

	return diss.loop
}

func (d *disseminator) Handle(ctx context.Context, event loops.EventDiss) error {
	switch e := event.(type) {
	case *loops.LookupRequest:
		d.handleLookupRequest(e)
	case *loops.LockRequest:
		d.handleAcquireRequest(e)
	case *loops.ReportHost:
		d.handleReportHost(e)
	case *loops.StreamOrder:
		if d.v2 {
			return d.handleOrderV2(ctx, e)
		}
		return d.handleOrder(ctx, e)
	case *loops.DisseminationTimeout:
		d.handleTimeout(ctx, e)
	case *loops.Shutdown:
		d.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown disseminator event type: %T", e))
	}

	return nil
}

func (d *disseminator) handleShutdown(shutdown *loops.Shutdown) {
	defer d.wg.Wait()

	d.streamLoop.Close(shutdown)
	d.inflight.Close(shutdown.Error)
	d.timeoutQ.Close()

	stream.LoopFactory.CacheLoop(d.streamLoop)
	loopCache.Put(d)
}

func (d *disseminator) handleTimeout(ctx context.Context, timeout *loops.DisseminationTimeout) {
	if d.v2 {
		// v2 timeouts are keyed by round seq; only in-flight rounds count.
		if _, ok := d.v2Rounds[timeout.Version]; !ok {
			return
		}
	} else if timeout.Version != d.timeoutVersion {
		// Ignore old timeouts.
		return
	}

	log.Warnf("Dissemination timeout for version %d, closing stream to reconnect", timeout.Version)

	// Close the stream rather than killing the placement subsystem. The recv
	// goroutine will exit and enqueue ConnCloseStream to the placement loop,
	// which will shut down this disseminator, halt actors, and reconnect to
	// placement. This ensures actor operations are unblocked promptly instead
	// of hanging until the server-side timeout resolves the slow peer.
	d.streamLoop.Close(&loops.Shutdown{
		Error: fmt.Errorf("dissemination timeout after %s for version %d",
			d.timeout,
			timeout.Version,
		),
	})
}
