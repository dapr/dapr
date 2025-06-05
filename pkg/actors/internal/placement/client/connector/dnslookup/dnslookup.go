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

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/actors/internal/placement/client/connector"

	"google.golang.org/grpc"
)

type lookupFunc func(ctx context.Context, host string) (addrs []string, err error)

type dnsLookUpConnector struct {
	dnsEntries []string
	current    string
	host       string
	port       string
	gOpts      []grpc.DialOption
	resolver   lookupFunc
}

var log = logger.NewLogger("dapr.runtime.actors.placement.client.connector.dnslookup")

func New(opts Options) (connector.Interface, error) {
	host, port, err := net.SplitHostPort(opts.Address)
	if err != nil {
		return nil, err
	}

	resolver := opts.resolver
	if opts.resolver == nil {
		resolver = net.Resolver{PreferGo: true}.LookupHost
	}

	return &dnsLookUpConnector{
		host:     host,
		port:     port,
		gOpts:    opts.GRPCOptions,
		resolver: resolver,
	}, nil
}

type Options struct {
	GRPCOptions []grpc.DialOption
	Address     string
	resolver    lookupFunc
}

func (r *dnsLookUpConnector) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	if len(r.dnsEntries) == 0 {
		if err := r.refreshEntries(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh DNS addresses: %w", err)
		}
	}

	hostPort := net.JoinHostPort(r.dnsEntries[0], r.port)
	r.current = hostPort
	r.dnsEntries = r.dnsEntries[1:]

	log.Debugf("Attempting to connect to placement %s", hostPort)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, hostPort, r.gOpts...)
	if err != nil {
		return nil, err
	}

	log.Infof("Connected to placement %s", hostPort)

	return conn, nil
}

func (r *dnsLookUpConnector) Address() string {
	return r.current
}

func (r *dnsLookUpConnector) refreshEntries(ctx context.Context) error {
	addrs, err := r.resolver(ctx, r.host)
	if err != nil {
		return fmt.Errorf("failed to lookup addresses: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no addresses found for %s", r.host)
	}

	r.dnsEntries = addrs
	return nil
}
