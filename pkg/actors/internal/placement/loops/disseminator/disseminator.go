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
	"time"

	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/timeout"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.loops.disseminator")

var (
	LoopFactoryCache = loop.New[loops.Event](1024)
	loopCache        = sync.Pool{New: func() any {
		return new(disseminator)
	}}
)

type Options struct {
	Channel       v1pb.Placement_ReportDaprStatusClient
	PlacementLoop loop.Interface[loops.Event]
	ActorTable    table.Interface
	Scheduler     schedclient.Reloader
	IDx           uint64
	HTarget       healthz.Target

	DisseminationTimeout time.Duration
	Cancel               context.CancelCauseFunc

	Inflight  *inflight.Inflight
	Namespace string
	ID        string
}

type disseminator struct {
	namespace string
	id        string

	loop         loop.Interface[loops.Event]
	inflight     *inflight.Inflight
	actorTable   table.Interface
	scheduler    schedclient.Reloader
	healthTarget healthz.Target

	timeout        time.Duration
	timeoutQ       *timeout.Timeout
	cancel         context.CancelCauseFunc
	timeoutVersion uint64

	streamLoop loop.Interface[loops.Event]

	wg sync.WaitGroup

	currentOperation v1pb.HostOperation
	currentVersion   uint64
}

func New(ctx context.Context, opts Options) loop.Interface[loops.Event] {
	diss := loopCache.Get().(*disseminator)

	diss.namespace = opts.Namespace
	diss.id = opts.ID
	diss.actorTable = opts.ActorTable
	diss.scheduler = opts.Scheduler

	diss.currentOperation = v1pb.HostOperation_LOCK
	diss.currentVersion = 0
	diss.healthTarget = opts.HTarget

	diss.loop = LoopFactoryCache.NewLoop(diss)
	diss.inflight = opts.Inflight

	diss.timeout = opts.DisseminationTimeout
	diss.timeoutQ = timeout.New(timeout.Options{
		Loop:    diss.loop,
		Timeout: opts.DisseminationTimeout,
	})
	diss.cancel = opts.Cancel

	diss.streamLoop = stream.New(ctx, stream.Options{
		Channel:       opts.Channel,
		PlacementLoop: opts.PlacementLoop,
		IDx:           opts.IDx,
	})

	diss.wg.Add(1)
	go func() {
		defer diss.wg.Done()
		derr := diss.streamLoop.Run(ctx)
		if derr != nil {
			log.Errorf("Stream loop ended with error: %s", derr)
		}
	}()

	return diss.loop
}

func (d *disseminator) Handle(ctx context.Context, event loops.Event) error {
	switch e := event.(type) {
	case *loops.LookupRequest:
		d.handleLookupRequest(e)
	case *loops.LockRequest:
		d.handleAcquireRequest(e)
	case *loops.ReportHost:
		d.handleReportHost(e)
	case *loops.StreamOrder:
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
	d.inflight.Lock(shutdown.Error)
	d.timeoutQ.Close()

	stream.LoopFactory.CacheLoop(d.streamLoop)
	loopCache.Put(d)
}

func (d *disseminator) handleTimeout(ctx context.Context, timeout *loops.DisseminationTimeout) {
	if timeout.Version != d.timeoutVersion {
		// Ignore old timeouts.
		return
	}

	log.Warnf("Dissemination timeout for version %d, shutting down", timeout.Version)

	d.cancel(fmt.Errorf("dissemination timeout after %s for version %d",
		d.timeout,
		timeout.Version,
	))
}
