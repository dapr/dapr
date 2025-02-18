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
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/internal/placement/lock"
	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

const grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`

var log = logger.NewLogger("dapr.runtime.actors.placement.client")

type Options struct {
	Addresses []string
	Security  security.Handler
	Lock      *lock.Lock
	Table     table.Interface
	Healthz   healthz.Healthz
}

type Client struct {
	grpcOpts     []grpc.DialOption
	addressIndex int
	addresses    []string
	htarget      healthz.Target

	table       table.Interface
	lock        *lock.Lock
	reconnectCh chan chan error

	conn   *grpc.ClientConn
	stream atomic.Pointer[v1pb.Placement_ReportDaprStatusClient]

	readyCh chan struct{}
	ready   atomic.Bool
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
		grpcOpts:    gopts,
		lock:        opts.Lock,
		addresses:   addresses,
		reconnectCh: make(chan chan error, 1),
		readyCh:     make(chan struct{}),
		table:       opts.Table,
		htarget:     opts.Healthz.AddTarget(),
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	connCtx, connCancel := context.WithCancel(ctx)
	if err := c.connectRoundRobin(connCtx); err != nil {
		connCancel()
		return err
	}

	c.ready.Store(true)
	close(c.readyCh)
	c.htarget.Ready()

	for {
		select {
		case ch := <-c.reconnectCh:
			c.ready.Store(false)
			unlock := c.lock.Lock()
			connCancel()

			if err := c.table.HaltAll(); err != nil {
				unlock()
				log.Errorf("Error whilst deactivating all actors when shutting down client: %s", err)
				return nil
			}

			if ctx.Err() != nil {
				return ctx.Err()
			}

			connCtx, connCancel = context.WithCancel(ctx)
			err := c.connectRoundRobin(connCtx)
			ch <- err
			unlock()
			if err != nil {
				connCancel()
				return err
			}

			c.ready.Store(true)

		case <-ctx.Done():
			c.ready.Store(false)
			connCancel()
			unlock := c.lock.Lock()
			defer unlock()
			if err := c.table.HaltAll(); err != nil {
				log.Errorf("Error whilst deactivating all actors when shutting down client: %s", err)
			}
			return nil
		}
	}
}

func (c *Client) Ready() bool {
	return c.ready.Load()
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
			return err
		}
	}

	stream, err := v1pb.NewPlacementClient(c.conn).ReportDaprStatus(ctx)
	if err != nil {
		return err
	}
	c.stream.Store(&stream)

	log.Infof("Connected to placement %s", c.addresses[c.addressIndex%len(c.addresses)])

	return nil
}

func (c *Client) Recv(ctx context.Context) (*v1pb.PlacementOrder, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.readyCh:
	}

	for {
		o, err := (*c.stream.Load()).Recv()
		if err == nil {
			return o, nil
		}

		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		s, ok := status.FromError(err)

		if (ok &&
			(s.Code() == codes.FailedPrecondition || s.Message() == "placement service is closed")) ||
			errors.Is(err, io.EOF) {
			log.Infof("Placement service is closed, reconnecting...")
			errCh := make(chan error, 1)
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}

			c.reconnectCh <- errCh

			select {
			case <-ctx.Done():
				err = ctx.Err()
			case err = <-errCh:
			}
			if err != nil {
				return nil, err
			}

			continue
		}

		return nil, err
	}
}

func (c *Client) Send(ctx context.Context, host *v1pb.Host) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.readyCh:
	}

	for {
		err := (*c.stream.Load()).Send(host)
		if err == nil {
			return nil
		}

		log.Errorf("Failed to send host report to placement, retrying: %s", err)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
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
