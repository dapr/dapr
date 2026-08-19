/*
Copyright 2024 The Dapr Authors
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
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/dnslookup"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/leader"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/static"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	loopsplacement "github.com/dapr/dapr/pkg/actors/internal/placement/loops/placement"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport"
	transportv1 "github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport/v1"
	transportv2 "github.com/dapr/dapr/pkg/actors/internal/placement/loops/stream/transport/v2"
	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement")

type Interface interface {
	Run(context.Context) error
	Lock(ctx context.Context, actorType string) (context.Context, context.CancelCauseFunc, error)
	LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error)
	IsActorHosted(ctx context.Context, actorType, actorID string) bool
	Ready() bool
	SetDrainOngoingCallTimeout(drain *bool, timeout *time.Duration)
	SetEntityDrainOngoingCallTimeouts(timeouts map[string]time.Duration)
}

type Options struct {
	AppID     string
	Namespace string
	Hostname  string
	Port      int
	Addresses []string

	Scheduler schedclient.Reloader
	Security  security.Handler
	Table     table.Interface
	Healthz   healthz.Healthz
	Mode      modes.DaprMode

	// DisseminationTimeout is the daprd-side timeout for a placement
	// LOCK -> UPDATE -> UNLOCK round. If the round exceeds this, daprd
	// resets its placement stream and halts hosted actors.
	DisseminationTimeout time.Duration

	// SchedulerPlacement asks the scheduler whether it serves placement and,
	// if so, uses it as this sidecar's placement authority. Otherwise the
	// standalone placement service is used when Addresses are configured.
	// The choice is made once on startup.
	SchedulerPlacement bool

	// SchedulerLeadership tracks the scheduler placement leader. Required
	// when SchedulerPlacement is set.
	SchedulerLeadership *leadership.Leadership
}

type placement struct {
	hostname string
	port     string
	table    table.Interface
	ready    *atomic.Bool

	loop loop.Interface[loops.EventPlace]
}

func New(opts Options) (Interface, error) {
	hasAddresses := len(opts.Addresses) > 0 &&
		(len(opts.Addresses) != 1 || strings.TrimSpace(strings.Trim(opts.Addresses[0], `"'`)) != "")

	if !opts.SchedulerPlacement && !hasAddresses {
		return nil, errors.New("no placement addresses provided")
	}

	// v1Setup builds the connector and stream factory speaking to the
	// standalone placement service.
	v1Setup := func() (connector.Interface, streamFactory, error) {
		placementID, err := spiffeid.FromSegments(
			opts.Security.ControlPlaneTrustDomain(),
			"ns", opts.Security.ControlPlaneNamespace(), "dapr-placement",
		)
		if err != nil {
			return nil, nil, err
		}

		gopts := grpcOptions(opts.Security, placementID)

		var conn connector.Interface
		switch opts.Mode {
		case modes.KubernetesMode:
			// In Kubernetes environment, dapr-placement headless service resolves multiple IP addresses.
			// With round robin load balancer, Dapr can find the leader automatically.
			conn, err = dnslookup.New(dnslookup.Options{
				Address:     opts.Addresses[0],
				GRPCOptions: gopts,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create roundrobin client: %w", err)
			}
		default:
			// In non-Kubernetes environment, will round robin over the provided addresses
			conn, err = static.New(static.Options{
				Addresses:   opts.Addresses,
				GRPCOptions: gopts,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create roundrobin client: %w", err)
			}
		}

		factory := func(ctx context.Context, cc *grpc.ClientConn) (transport.Transport, error) {
			channel, err := v1pb.NewPlacementClient(cc).ReportDaprStatus(ctx)
			if err != nil {
				return nil, err
			}
			return transportv1.New(transportv1.Options{
				Channel:   channel,
				AppID:     opts.AppID,
				Namespace: opts.Namespace,
			}), nil
		}

		return conn, factory, nil
	}

	var conn connector.Interface
	var factory streamFactory
	var fallback *loopsplacement.Fallback
	var err error

	if opts.SchedulerPlacement {
		if opts.SchedulerLeadership == nil {
			return nil, errors.New("scheduler leadership tracker is required for SchedulerPlacement")
		}

		var schedulerID spiffeid.ID
		schedulerID, err = spiffeid.FromSegments(
			opts.Security.ControlPlaneTrustDomain(),
			"ns", opts.Security.ControlPlaneNamespace(), "dapr-scheduler",
		)
		if err != nil {
			return nil, err
		}

		conn = leader.New(leader.Options{
			Leadership:  opts.SchedulerLeadership,
			GRPCOptions: grpcOptions(opts.Security, schedulerID),
		})
		factory = func(ctx context.Context, cc *grpc.ClientConn) (transport.Transport, error) {
			channel, cerr := schedulerv1pb.NewSchedulerClient(cc).ReportActorTypes(ctx)
			if cerr != nil {
				return nil, cerr
			}
			return transportv2.New(transportv2.Options{
				Channel: channel,
			}), nil
		}

		// The placement service is the startup fallback for scheduler
		// clusters which do not serve placement. It is dropped once an
		// authority is chosen.
		if hasAddresses {
			fconn, ffactory, ferr := v1Setup()
			if ferr != nil {
				return nil, ferr
			}
			fallback = &loopsplacement.Fallback{
				Connector:          fconn,
				StreamFactory:      ffactory,
				SchedulerPlacement: false,
			}
		}

		log.Info("Scheduler address configured; asking the scheduler whether it serves actor placement")
	} else {
		conn, factory, err = v1Setup()
		if err != nil {
			return nil, err
		}
	}

	var ready atomic.Bool

	return &placement{
		ready:    &ready,
		hostname: opts.Hostname,
		port:     strconv.Itoa(opts.Port),
		table:    opts.Table,
		loop: loopsplacement.New(loopsplacement.Options{
			Ready:      &ready,
			ActorTable: opts.Table,
			Scheduler:  opts.Scheduler,
			Hostname:   opts.Hostname,
			Port:       strconv.Itoa(opts.Port),
			ID:         opts.AppID,
			Namespace:  opts.Namespace,
			Healthz:    opts.Healthz,
			Connector:  conn,
			InitialReport: &loops.Report{
				Address:   net.JoinHostPort(opts.Hostname, strconv.Itoa(opts.Port)),
				AppID:     opts.AppID,
				Namespace: opts.Namespace,
			},
			StreamFactory:        factory,
			SchedulerPlacement:   opts.SchedulerPlacement,
			Fallback:             fallback,
			DisseminationTimeout: opts.DisseminationTimeout,
		}),
	}, nil
}

type streamFactory = func(ctx context.Context, conn *grpc.ClientConn) (transport.Transport, error)

func grpcOptions(sec security.Handler, id spiffeid.ID) []grpc.DialOption {
	gopts := []grpc.DialOption{sec.GRPCDialOptionMTLS(id)}

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		gopts = append(
			gopts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	return gopts
}

func (p *placement) Run(ctx context.Context) error {
	ch, atypes := p.table.SubscribeToTypeUpdates(ctx)
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		if err := p.table.HaltAll(cctx); err != nil {
			log.Errorf("Failed to halt all actors during placement shutdown: %v", err)
		}
	}()

	return concurrency.NewRunnerManager(
		p.loop.Run,
		func(ctx context.Context) error {
			p.loop.Enqueue(&loops.PlacementReconnect{
				ActorTypes: new(atypes),
			})
			for {
				select {
				case atypes := <-ch:
					p.loop.Enqueue(&loops.UpdateTypes{
						ActorTypes: atypes,
					})
				case <-ctx.Done():
					log.Info("Placement client shutting down")
					p.loop.Close(&loops.Shutdown{Error: ctx.Err()})
					return ctx.Err()
				}
			}
		},
	).Run(ctx)
}

func (p *placement) Lock(ctx context.Context, actorType string) (context.Context, context.CancelCauseFunc, error) {
	ch := make(chan *loops.LockResponse, 1)
	p.loop.Enqueue(&loops.LockRequest{
		ActorType: actorType,
		Context:   ctx,
		Response:  ch,
	})

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case resp := <-ch:
		if resp.Context.Err() != nil {
			return nil, nil, resp.Context.Err()
		}
		return resp.Context, resp.Cancel, nil
	}
}

func (p *placement) Ready() bool {
	return p.ready.Load()
}

func (p *placement) LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error) {
	ch := make(chan *loops.LookupResponse, 1)
	p.loop.Enqueue(&loops.LookupRequest{
		Context:  ctx,
		Request:  req,
		Response: ch,
	})

	select {
	case <-ctx.Done():
		return nil, nil, nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, nil, nil, resp.Error
		}
		if resp.Context.Err() != nil {
			return nil, nil, nil, resp.Context.Err()
		}
		return resp.Response, resp.Context, resp.Cancel, nil
	}
}

func (p *placement) IsActorHosted(ctx context.Context, actorType, actorID string) bool {
	lar, _, cancel, err := p.LookupActor(ctx, &api.LookupActorRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})
	if err != nil {
		log.Errorf("failed to lookup actor %s/%s: %s", actorType, actorID, err)
		return false
	}
	cancel(nil)

	return lar != nil && loops.IsActorLocal(lar.Address, p.hostname, p.port)
}

func (p *placement) SetDrainOngoingCallTimeout(drain *bool, timeout *time.Duration) {
	p.loop.Enqueue(&loops.SetDrainOngoingCallTimeout{
		Drain:   drain,
		Timeout: timeout,
	})
}

func (p *placement) SetEntityDrainOngoingCallTimeouts(timeouts map[string]time.Duration) {
	p.loop.Enqueue(&loops.SetEntityDrainOngoingCallTimeouts{
		Timeouts: timeouts,
	})
}
