/*
Copyright 2022 The Dapr Authors
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

package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/internal/placement/client/connector"
	"github.com/dapr/dapr/pkg/actors/internal/placement/client/connector/dnslookup"
	"github.com/dapr/dapr/pkg/actors/internal/placement/client/connector/static"
	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	schedclient "github.com/dapr/dapr/pkg/runtime/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/concurrency/lock"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.client")

type Options struct {
	Addresses   []string
	Security    security.Handler
	Lock        *lock.OuterCancel
	Table       table.Interface
	Healthz     healthz.Healthz
	InitialHost *v1pb.Host
	Mode        modes.DaprMode
	Scheduler   schedclient.Reloader
}

type Client struct {
	htarget healthz.Target

	table table.Interface
	lock  *lock.OuterCancel

	connector connector.Interface
	client    v1pb.Placement_ReportDaprStatusClient
	sendQueue chan *v1pb.Host
	recvQueue chan *v1pb.PlacementOrder

	scheduler     schedclient.Reloader
	currentReport atomic.Pointer[[]string]
	toReport      atomic.Pointer[[]string]

	ready      atomic.Bool
	retryCount uint32
}

// New creates a new placement client for the given dial opts.
func New(opts Options) (*Client, error) {
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

	return &Client{
		lock:      opts.Lock,
		connector: conn,
		sendQueue: make(chan *v1pb.Host),
		recvQueue: make(chan *v1pb.PlacementOrder),
		table:     opts.Table,
		htarget:   opts.Healthz.AddTarget(),
		scheduler: opts.Scheduler,
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	c.currentReport.Store(ptr.Of([]string{}))
	c.toReport.Store(ptr.Of([]string{}))

	if err := c.connectRoundRobin(ctx); err != nil {
		return err
	}

	c.htarget.Ready()
	c.ready.Store(true)

	runner := func() *concurrency.RunnerManager {
		return concurrency.NewRunnerManager(
			func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case host := <-c.sendQueue:

						c.toReport.Store(ptr.Of(host.GetEntities()))

						if err := c.client.Send(host); err != nil {
							return err
						}

						c.htarget.Ready()
						c.ready.Store(true)
					}
				}
			},
			func(ctx context.Context) error {
				for {
					order, err := c.client.Recv()
					if err != nil {
						return err
					}

					if order.GetOperation() == "unlock" &&
						!slices.Equal(*c.toReport.Load(), *c.currentReport.Load()) {
						c.scheduler.ReloadActorTypes(*c.toReport.Load())
						c.currentReport.Store(c.toReport.Load())
					}

					select {
					case <-ctx.Done():
						return ctx.Err()
					case c.recvQueue <- order:
					}
				}
			},
		)
	}

	for {
		err := runner().Run(ctx)

		c.scheduler.ReloadActorTypes([]string{})
		c.currentReport.Store(ptr.Of([]string{}))
		c.toReport.Store(ptr.Of([]string{}))

		if ctx.Err() != nil {
			return c.table.HaltAll()
		}

		c.ready.Store(false)
		c.retryCount++
		// Re-enable once healthz of daprd is not tired to liveness.
		// c.htarget.NotReady()

		if status.Code(err) == codes.FailedPrecondition && c.retryCount%10 != 0 {
			log.Debugf("Error communicating with placement: %s", err)
		} else {
			log.Errorf("Error communicating with placement: %s", err)
			c.retryCount = 0
		}

		if err = c.handleReconnect(ctx); err != nil {
			log.Errorf("Failed to reconnect to placement: %s", err)
			return nil
		}
	}
}

func (c *Client) Ready() bool {
	return c.ready.Load()
}

func (c *Client) handleReconnect(ctx context.Context) error {
	unlock := c.lock.Lock()
	defer unlock()

	log.Info("Placement stream disconnected")

	if err := c.table.HaltAll(); err != nil {
		return fmt.Errorf("error whilst deactivating all actors when shutting down client: %s", err)
	}

	log.Info("Halted all actors on this host")

	if ctx.Err() != nil {
		return ctx.Err()
	}

	log.Infof("Reconnecting to placement...")

	if err := c.connectRoundRobin(ctx); err != nil {
		return err
	}

	log.Infof("Reconnected to placement")

	return nil
}

func (c *Client) connectRoundRobin(ctx context.Context) error {
	for {
		err := c.connect(ctx)
		if err == nil {
			return nil
		}

		log.Errorf("Failed to connect to placement %s: %s", c.connector.Address(), err)

		select {
		case <-time.After(time.Second / 2):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	conn, err := c.connector.Connect(ctx)
	if err != nil {
		return err
	}

	client, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(ctx)
	if err != nil {
		err = fmt.Errorf("failed to create placement client: %w", err)
		return err
	}

	c.client = client

	log.Infof("Connected to placement %s", c.connector.Address())

	return nil
}

func (c *Client) Recv(ctx context.Context) (*v1pb.PlacementOrder, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case order := <-c.recvQueue:
		return order, nil
	}
}

func (c *Client) Send(ctx context.Context, host *v1pb.Host) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case c.sendQueue <- host:
		return nil
	}
}
