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

package placement

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/leader"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/retry"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.loops.placement")

type Options struct {
	Hostname  string
	Port      string
	ID        string
	Namespace string

	Ready *atomic.Bool

	Healthz       healthz.Healthz
	Connector     connector.Interface
	InitialReport *loops.Report

	// StreamFactory opens a placement stream on an established connection,
	// selecting the wire protocol (v1 placement service, v2 scheduler).
	StreamFactory func(ctx context.Context, conn *grpc.ClientConn) (transport.Transport, error)

	// V2 speaks the v2 (scheduler placement) protocol on the stream.
	V2 bool

	// Fallback, when non-nil, is the v1 placement service connector to use
	// when the scheduler cluster reports it does not serve placement. It is
	// dropped once a placement authority has been chosen, so a sidecar never
	// changes authority while running.
	Fallback *Fallback

	ActorTable table.Interface
	Scheduler  schedclient.Reloader

	DisseminationTimeout time.Duration
}

// Fallback is a secondary connector and stream factory to switch to when the
// primary is unsupported by the cluster.
type Fallback struct {
	Connector     connector.Interface
	StreamFactory func(ctx context.Context, conn *grpc.ClientConn) (transport.Transport, error)
	V2            bool
}

// swapAlt exchanges the active connector with the kept alternative.
func (p *placement) swapAlt() {
	p.alt, p.connector, p.streamFactory, p.v2 = &Fallback{
		Connector:     p.connector,
		StreamFactory: p.streamFactory,
		V2:            p.v2,
	}, p.alt.Connector, p.alt.StreamFactory, p.alt.V2
}

type placement struct {
	id        string
	namespace string

	ready *atomic.Bool

	actorTable table.Interface
	scheduler  schedclient.Reloader

	inflight  *inflight.Inflight
	connector connector.Interface
	loop      loop.Interface[loops.EventPlace]
	htarget   healthz.Target

	lookups []loops.EventLookup

	idx uint64

	dissLoop      loop.Interface[loops.EventDiss]
	report        *loops.Report
	streamFactory func(ctx context.Context, conn *grpc.ClientConn) (transport.Transport, error)
	v2            bool
	fallback      *Fallback

	// alt is the connector of whichever placement authority is not active,
	// so an authority handing over is adopted live: every actor was already
	// halted when the previous stream closed.
	alt *Fallback

	dissTimeout time.Duration

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.EventPlace] {
	place := &placement{
		id:            opts.ID,
		ready:         opts.Ready,
		namespace:     opts.Namespace,
		connector:     opts.Connector,
		report:        opts.InitialReport,
		streamFactory: opts.StreamFactory,
		v2:            opts.V2,
		fallback:      opts.Fallback,
		htarget:       opts.Healthz.AddTarget("internal-placement-service"),
		inflight: inflight.New(inflight.Options{
			Hostname: opts.Hostname,
			Port:     opts.Port,
		}),
		actorTable:  opts.ActorTable,
		scheduler:   opts.Scheduler,
		dissTimeout: opts.DisseminationTimeout,
	}
	place.loop = loop.New[loops.EventPlace](8).NewLoop(place)
	return place.loop
}

func (p *placement) Handle(ctx context.Context, event loops.EventPlace) error {
	switch e := event.(type) {
	case *loops.StreamOrder:
		p.handleOrder(e)
	case *loops.LookupRequest:
		p.handleLookupRequest(e)
	case *loops.LockRequest:
		p.handleLockRequest(e)
	case *loops.PlacementReconnect:
		return p.handleReconnect(ctx, e)
	case *loops.ConnCloseStream:
		return p.handleCloseStream(ctx, e)
	case *loops.UpdateTypes:
		p.handleUpdateTypes(e)
	case *loops.SetDrainOngoingCallTimeout:
		p.handleSetDrainOngoingCallTimeout(e)
	case *loops.SetEntityDrainOngoingCallTimeouts:
		p.inflight.SetEntityDrainOngoingCallTimeouts(e.Timeouts)
	case *loops.Shutdown:
		p.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown placement event type: %T", e))
	}

	return nil
}

func (p *placement) handleUpdateTypes(up *loops.UpdateTypes) {
	p.report.ActorTypes = up.ActorTypes

	// No stream yet. The types go out with the initial report on connect.
	if p.dissLoop == nil {
		return
	}

	p.dissLoop.Enqueue(&loops.ReportHost{
		Report: p.report.Clone(),
	})
}

func (p *placement) handleOrder(order *loops.StreamOrder) {
	if p.idx != order.IDx {
		log.Debugf("Dropping order from placement idx %d, current idx is %d", order.IDx, p.idx)
		return
	}
	p.dissLoop.Enqueue(order)
}

func (p *placement) handleLookupRequest(req *loops.LookupRequest) {
	if p.dissLoop == nil {
		p.lookups = append(p.lookups, req)
		return
	}
	p.dissLoop.Enqueue(req)
}

func (p *placement) handleLockRequest(req *loops.LockRequest) {
	if p.dissLoop == nil {
		p.lookups = append(p.lookups, req)
		return
	}
	p.dissLoop.Enqueue(req)
}

func (p *placement) handleReconnect(ctx context.Context, recon *loops.PlacementReconnect) error {
	var client transport.Transport
	var err error
	var unavailableLogged bool
	for {
		client, err = p.tryConnect(ctx)
		if err == nil {
			break
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		// The scheduler cluster does not serve placement. Only reachable
		// before an authority is chosen: the fallback is cleared on the
		// first successful connect.
		if errors.Is(err, leader.ErrSchedulerPlacementUnsupported) {
			if p.fallback != nil {
				log.Warn("Scheduler cluster does not serve actor placement, using the placement service")
				p.alt = &Fallback{
					Connector:     p.connector,
					StreamFactory: p.streamFactory,
					V2:            p.v2,
				}
				p.connector = p.fallback.Connector
				p.streamFactory = p.fallback.StreamFactory
				p.v2 = p.fallback.V2
				p.fallback = nil
				continue
			}

			// The cluster no longer serves scheduler placement: try the
			// placement service. Only this explicit signal defects, a
			// leaderless scheduler failover blocks in the connector instead.
			if p.alt != nil {
				p.swapAlt()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retry.Jitter(time.Second/2, time.Second/4)):
				}
				continue
			}

			// Nothing can place actors right now: come up with actor APIs
			// disconnected and quietly retry until a scheduler serves
			// placement.
			if !unavailableLogged {
				unavailableLogged = true
				log.Error("Actor placement unavailable: the scheduler does not serve actor placement and no placement service is available to this sidecar. Actor and Workflow APIs will be unavailable until the scheduler serves placement")
				p.ready.Store(false)
				p.htarget.Ready()
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retry.Jitter(time.Second*3, time.Second)):
			}
			continue
		}

		log.Errorf("Failed to connect to placement service: %s. Retrying...", err)

		// The active authority may have handed over: probe the other one.
		if p.alt != nil {
			p.swapAlt()
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retry.Jitter(time.Second/2, time.Second/4)):
		}
	}

	// The other authority's connector is kept, so a later handover in
	// either direction is adopted without a restart.
	if p.fallback != nil {
		p.alt = p.fallback
		p.fallback = nil
	}

	if recon.TransientPrior {
		log.Debugf("Connected to placement service: %s", p.connector.Address())
	} else {
		log.Infof("Connected to placement service: %s", p.connector.Address())
	}

	p.idx++

	p.dissLoop = disseminator.New(ctx, disseminator.Options{
		ID:                   p.id,
		Namespace:            p.namespace,
		Inflight:             p.inflight,
		Channel:              client,
		PlacementLoop:        p.loop,
		IDx:                  p.idx,
		ActorTable:           p.actorTable,
		Scheduler:            p.scheduler,
		HTarget:              p.htarget,
		DisseminationTimeout: p.dissTimeout,
		Ready:                p.ready,
		V2:                   p.v2,
	})

	p.wg.Go(func() {
		derr := p.dissLoop.Run(ctx)
		if derr != nil {
			log.Errorf("Placement dissemination loop exited with error: %s", derr)
		}
	})

	if recon.ActorTypes != nil {
		p.report.ActorTypes = *recon.ActorTypes
	}

	if recon.TransientPrior {
		log.Debugf("Reporting initial host to placement service with initial types %v", p.report.ActorTypes)
	} else {
		log.Infof("Reporting initial host to placement service with initial types %v", p.report.ActorTypes)
	}
	p.dissLoop.Enqueue(&loops.ReportHost{
		Report: p.report.Clone(),
	})

	for _, l := range p.lookups {
		p.dissLoop.Enqueue(l.(loops.EventDiss))
	}
	p.lookups = nil

	return nil
}

func (p *placement) handleCloseStream(ctx context.Context, closeStream *loops.ConnCloseStream) error {
	if closeStream.IDx != p.idx {
		log.Infof("Ignoring close stream for idx %d, current idx is %d", closeStream.IDx, p.idx)
		return nil
	}

	p.ready.Store(false)

	p.dissLoop.Close(&loops.Shutdown{
		Error: closeStream.Error,
	})
	p.wg.Wait()
	disseminator.LoopFactoryCache.CacheLoop(p.dissLoop)

	// A new stream session starts from its authoritative snapshot.
	p.inflight.ResetSession()

	if err := p.actorTable.HaltAll(ctx); err != nil {
		log.Errorf("Failed to halt all actors during placement disconnection: %v", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Classify this close so the matching reconnect's per-cycle log lines
	// (Connected to placement service, Reporting initial host) follow the
	// same level. Threaded onto the PlacementReconnect event we hand to
	// handleReconnect rather than carried as receiver state, so there's no
	// stale carry-over between unrelated close events.
	transient := loops.IsTransientLeaderError(closeStream.Error)
	if transient {
		log.Debugf("Placement stream closed: %v. Reconnecting...", closeStream.Error)
	} else {
		log.Infof("Placement stream closed: %v. Reconnecting...", closeStream.Error)
	}
	return p.handleReconnect(ctx, &loops.PlacementReconnect{TransientPrior: transient})
}

func (p *placement) handleShutdown(shutdown *loops.Shutdown) {
	defer p.wg.Wait()

	if p.dissLoop == nil {
		return
	}

	p.dissLoop.Close(shutdown)
}

func (p *placement) handleSetDrainOngoingCallTimeout(event *loops.SetDrainOngoingCallTimeout) {
	p.inflight.SetDrainOngoingCallTimeout(event.Drain, event.Timeout)
}

func (p *placement) tryConnect(ctx context.Context) (transport.Transport, error) {
	conn, err := p.connector.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to placement service: %w", err)
	}

	client, err := p.streamFactory(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream to placement service: %w", err)
	}

	return client, nil
}
