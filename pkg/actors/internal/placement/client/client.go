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
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/concurrency/lock"
	"github.com/dapr/kit/logger"
)

const grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`

var log = logger.NewLogger("dapr.runtime.actors.placement.client")

type Options struct {
	Addresses   []string
	Security    security.Handler
	Lock        *lock.OuterCancel
	Table       table.Interface
	Healthz     healthz.Healthz
	InitialHost *v1pb.Host
}

type Client struct {
	grpcOpts     []grpc.DialOption
	addressIndex int
	addresses    []string
	htarget      healthz.Target

	table table.Interface
	lock  *lock.OuterCancel

	conn      *grpc.ClientConn
	client    v1pb.Placement_ReportDaprStatusClient
	sendQueue chan *v1pb.Host
	recvQueue chan *v1pb.PlacementOrder

	ready atomic.Bool
}

// New creates a new placement client for the given dial opts.
func New(opts Options) (*Client, error) {
	if len(opts.Addresses) == 0 {
		return nil, errors.New("no placement addresses provided")
	}

	addresses := addDNSResolverPrefix(opts.Addresses)

	var gopts []grpc.DialOption
	placementID, err := spiffeid.FromSegments(
		opts.Security.ControlPlaneTrustDomain(),
		"ns", opts.Security.ControlPlaneNamespace(), "dapr-placement",
	)
	if err != nil {
		return nil, err
	}

	gopts = append(gopts, opts.Security.GRPCDialOptionMTLS(placementID))

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		gopts = append(
			gopts,
			grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()),
		)
	}

	if len(addresses) == 1 && strings.HasPrefix(addresses[0], "dns:///") {
		// In Kubernetes environment, dapr-placement headless service resolves multiple IP addresses.
		// With round robin load balancer, Dapr can find the leader automatically.
		gopts = append(gopts, grpc.WithDefaultServiceConfig(grpcServiceConfig))
	}

	return &Client{
		lock:      opts.Lock,
		addresses: addresses,
		grpcOpts:  gopts,
		sendQueue: make(chan *v1pb.Host),
		recvQueue: make(chan *v1pb.PlacementOrder),
		table:     opts.Table,
		htarget:   opts.Healthz.AddTarget(),
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	conctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := c.connectRoundRobin(conctx); err != nil {
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
		if err == nil {
			return c.table.HaltAll()
		}

		cancel()
		c.ready.Store(false)
		// Re-enable once healthz of daprd is not tired to liveness.
		// c.htarget.NotReady()

		log.Errorf("Error communicating with placement: %s", err)
		if ctx.Err() != nil {
			return c.table.HaltAll()
		}

		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}

		cancel, err = c.handleReconnect(ctx)
		if err != nil {
			log.Errorf("Failed to reconnect to placement: %s", err)
			return nil
		}
	}
}

func (c *Client) Ready() bool {
	return c.ready.Load()
}

func (c *Client) handleReconnect(ctx context.Context) (context.CancelFunc, error) {
	unlock := c.lock.Lock()
	defer unlock()

	log.Info("Placement stream disconnected")

	if err := c.table.HaltAll(); err != nil {
		return nil, fmt.Errorf("error whilst deactivating all actors when shutting down client: %s", err)
	}

	log.Info("Halted all actors on this host")

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	log.Infof("Reconnecting to placement...")

	ctx, cancel := context.WithCancel(ctx)
	if err := c.connectRoundRobin(ctx); err != nil {
		cancel()
		return nil, err
	}

	log.Infof("Reconnected to placement")

	return cancel, nil
}

func (c *Client) connectRoundRobin(ctx context.Context) error {
	for {
		err := c.connect(ctx)
		if err == nil {
			return nil
		}

		log.Errorf("Failed to connect to placement %s: %s", c.addresses[(c.addressIndex-1)%len(c.addresses)], err)

		select {
		case <-time.After(time.Second / 2):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	defer func() {
		c.addressIndex++
	}()

	log.Debugf("Attempting to connect to placement %s", c.addresses[c.addressIndex%len(c.addresses)])

	var err error
	// If we're trying to connect to the same address, we reuse the existing
	// connection.
	// This is not just an optimisation, but a necessary feature for the
	// round-robin load balancer to work correctly when connecting to the
	// placement headless service in k8s
	if c.conn == nil || len(c.addresses) > 1 {
		//nolint:staticcheck
		c.conn, err = grpc.DialContext(ctx, c.addresses[c.addressIndex%len(c.addresses)], c.grpcOpts...)
		if err != nil {
			return fmt.Errorf("failed to dial placement: %s", err)
		}
	}

	client, err := v1pb.NewPlacementClient(c.conn).ReportDaprStatus(ctx)
	if err != nil {
		err = fmt.Errorf("failed to create placement client: %s", err)
		if strings.Contains(err.Error(), "connect: connection refused") {
			// reset gRPC connenxt to reset the round robin load balancer state to
			// ensure we connect to new hosts if all Placement hosts have been
			// terminated.
			log.Infof("Resetting gRPC connection to reset round robin load balancer state")
			c.conn = nil
		}

		return err
	}

	c.client = client

	log.Infof("Connected to placement %s", c.addresses[c.addressIndex%len(c.addresses)])

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

// addDNSResolverPrefix add the `dns://` prefix to the given addresses
func addDNSResolverPrefix(addr []string) []string {
	resolvers := make([]string, 0, len(addr))
	for _, a := range addr {
		prefix := ""
		host, _, err := net.SplitHostPort(a)
		if err == nil && net.ParseIP(host) == nil {
			prefix = "dns:///"
		}
		resolvers = append(resolvers, prefix+a)
	}
	return resolvers
}
