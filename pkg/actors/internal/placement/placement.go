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
	"strconv"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/dnslookup"
	"github.com/dapr/dapr/pkg/actors/internal/placement/connector/static"
	"github.com/dapr/dapr/pkg/actors/internal/placement/loops"
	loopsplacement "github.com/dapr/dapr/pkg/actors/internal/placement/loops/placement"
	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.actors.placement")

type Interface interface {
	Run(context.Context) error
	Lock(context.Context) (context.Context, context.CancelCauseFunc, error)
	LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, context.Context, context.CancelCauseFunc, error)
	IsActorHosted(ctx context.Context, actorType, actorID string) bool
	Ready() bool
	SetDrainOngoingCallTimeout(drain *bool, timeout *time.Duration)
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
}

type placement struct {
	hostname string
	port     string
	table    table.Interface
	ready    *atomic.Bool
	errCh    chan error

	loop loop.Interface[loops.Event]
}

func New(opts Options) (Interface, error) {
	if len(opts.Addresses) == 0 {
		return nil, errors.New("no placement addresses provided")
	}

	placementID, err := spiffeid.FromSegments(
		opts.Security.ControlPlaneTrustDomain(),
		"ns", opts.Security.ControlPlaneNamespace(), "dapr-placement",
	)
	if err != nil {
		return nil, err
	}

	var gopts []grpc.DialOption
	gopts = append(gopts, opts.Security.GRPCDialOptionMTLS(placementID))

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		gopts = append(
			gopts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

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
			return nil, fmt.Errorf("failed to create roundrobin client: %w", err)
		}
	default:
		// In non-Kubernetes environment, will round robin over the provided addresses
		conn, err = static.New(static.Options{
			Addresses:   opts.Addresses,
			GRPCOptions: gopts,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create roundrobin client: %w", err)
		}
	}

	var ready atomic.Bool

	errCh := make(chan error, 1)

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
			InitialHost: &v1pb.Host{
				Name:      opts.Hostname + ":" + strconv.Itoa(opts.Port),
				Id:        opts.AppID,
				ApiLevel:  20,
				Namespace: opts.Namespace,
			},
			DisseminationTimeout: time.Second * 5,
			Cancel: func(cause error) {
				errCh <- cause
			},
		}),
	}, nil
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
			select {
			case err := <-p.errCh:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		func(ctx context.Context) error {
			p.loop.Enqueue(&loops.PlacementReconnect{
				ActorTypes: ptr.Of(atypes),
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

func (p *placement) Lock(ctx context.Context) (context.Context, context.CancelCauseFunc, error) {
	ch := make(chan *loops.LockResponse, 1)
	p.loop.Enqueue(&loops.LockRequest{
		Context:  ctx,
		Response: ch,
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
