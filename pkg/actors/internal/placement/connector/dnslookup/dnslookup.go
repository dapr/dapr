/*
Copyright 2025 The Dapr Authors
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

package dnslookup

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.manager.connector.dnslookup")

type lookupFunc func(ctx context.Context, host string) (addrs []string, err error)

type dnsLookUpConnector struct {
	// rrIndex tracks the round-robin position across DNS lookups so that
	// successive Connect calls cycle through available placement pods even
	// when DNS returns the same set.
	rrIndex  int
	current  string
	host     string
	port     string
	gOpts    []grpc.DialOption
	resolver lookupFunc
}

type Options struct {
	GRPCOptions []grpc.DialOption
	Address     string
	resolver    lookupFunc
}

func New(opts Options) (connector.Interface, error) {
	host, port, err := net.SplitHostPort(opts.Address)
	if err != nil {
		return nil, err
	}

	resolver := opts.resolver
	if opts.resolver == nil {
		resolver = (&net.Resolver{PreferGo: true}).LookupHost
	}

	return &dnsLookUpConnector{
		host:     host,
		port:     port,
		gOpts:    opts.GRPCOptions,
		resolver: resolver,
	}, nil
}

func (r *dnsLookUpConnector) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	// Re-resolve DNS on every connect attempt. Kubernetes headless services
	// return the current set of pod IPs. Previously, stale cached IPs from
	// terminated pods would cause 20-second dial timeouts during placement
	// pod restarts. DNS lookups are ~1ms and always return fresh IPs.
	addrs, err := r.resolver(ctx, r.host)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup addresses: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses found for %s", r.host)
	}

	// Round-robin across the resolved addresses. The modulo assignment keeps
	// rrIndex in [0, len(addrs)), so it never grows unbounded.
	r.rrIndex %= len(addrs)
	addr := addrs[r.rrIndex]
	r.rrIndex++

	hostPort := net.JoinHostPort(addr, r.port)
	r.current = hostPort

	log.Debugf("Attempting to connect to placement %s", hostPort)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, hostPort, r.gOpts...)
	if err != nil {
		return nil, err
	}

	log.Debugf("Connected to placement %s", hostPort)

	return conn, nil
}

func (r *dnsLookUpConnector) Address() string {
	return r.current
}
