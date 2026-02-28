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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/disseminator/inflight"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
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

	Healthz     healthz.Healthz
	Connector   connector.Interface
	InitialHost *v1pb.Host

	ActorTable table.Interface
	Scheduler  schedclient.Reloader

	DisseminationTimeout time.Duration
	Cancel               context.CancelCauseFunc
}

type placement struct {
	id        string
	namespace string

	ready *atomic.Bool

	actorTable table.Interface
	scheduler  schedclient.Reloader

	inflight  *inflight.Inflight
	connector connector.Interface
	loop      loop.Interface[loops.Event]
	htarget   healthz.Target

	lookups []loops.Event

	idx uint64

	dissLoop loop.Interface[loops.Event]
	host     *v1pb.Host

	dissTimeout time.Duration
	cancel      context.CancelCauseFunc

	wg sync.WaitGroup
}

func New(opts Options) loop.Interface[loops.Event] {
	place := &placement{
		id:        opts.ID,
		ready:     opts.Ready,
		namespace: opts.Namespace,
		connector: opts.Connector,
		host:      opts.InitialHost,
		htarget:   opts.Healthz.AddTarget("internal-placement-service"),
		inflight: inflight.New(inflight.Options{
			Hostname: opts.Hostname,
			Port:     opts.Port,
		}),
		actorTable:  opts.ActorTable,
		scheduler:   opts.Scheduler,
		dissTimeout: opts.DisseminationTimeout,
		cancel:      opts.Cancel,
	}
	place.loop = loop.New[loops.Event](8).NewLoop(place)
	return place.loop
}

func (p *placement) Handle(ctx context.Context, event loops.Event) error {
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
	case *loops.Shutdown:
		p.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown placement event type: %T", e))
	}

	return nil
}

func (p *placement) handleUpdateTypes(up *loops.UpdateTypes) {
	p.host.Entities = up.ActorTypes
	p.dissLoop.Enqueue(&loops.ReportHost{
		Host: proto.Clone(p.host).(*v1pb.Host),
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
	var client v1pb.Placement_ReportDaprStatusClient
	var err error
	for {
		client, err = p.tryConnect(ctx)
		if err == nil {
			break
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		log.Errorf("Failed to connect to placement service: %s. Retrying...", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second / 2):
		}
	}

	log.Infof("Connected to placement service: %s", p.connector.Address())

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
		Cancel:               p.cancel,
	})

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		derr := p.dissLoop.Run(ctx)
		if derr != nil {
			log.Errorf("Placement dissemination loop exited with error: %s", derr)
		}
	}()

	if recon.ActorTypes != nil {
		p.host.Entities = *recon.ActorTypes
	}

	log.Infof("Reporting initial host to placement service with initial types %v", p.host.GetEntities())
	p.dissLoop.Enqueue(&loops.ReportHost{
		Host: proto.Clone(p.host).(*v1pb.Host),
	})

	for _, l := range p.lookups {
		p.dissLoop.Enqueue(l)
	}
	p.lookups = nil

	p.ready.Store(true)

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

	if err := p.actorTable.HaltAll(ctx); err != nil {
		log.Errorf("Failed to halt all actors during placement disconnection: %v", err)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	log.Infof("Placement stream closed: %v. Reconnecting...", closeStream.Error)
	return p.handleReconnect(ctx, new(loops.PlacementReconnect))
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

func (p *placement) tryConnect(ctx context.Context) (v1pb.Placement_ReportDaprStatusClient, error) {
	conn, err := p.connector.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to placement service: %w", err)
	}

	client, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream to placement service: %w", err)
	}

	return client, nil
}
