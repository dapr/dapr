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

package roundrobin

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
)

type dnsroundrobin struct {
	dnsEntries []string
	host       string
	port       string
	gOpts      []grpc.DialOption
	resolver   *net.Resolver
}

type DNSOptions struct {
	GRPCOptions []grpc.DialOption
	Address     string
}

func (r *dnsroundrobin) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	if len(r.dnsEntries) == 0 {
		if err := r.refreshEntries(ctx); err != nil {
			return nil, fmt.Errorf("failed to refresh DNS addresses: %w", err)
		}
	}

	hostPort := r.Address()
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

func (r *dnsroundrobin) Address() string {
	return net.JoinHostPort(r.dnsEntries[0], r.port)
}

func (r *dnsroundrobin) refreshEntries(ctx context.Context) error {
	addrs, err := r.resolver.LookupHost(ctx, r.host)
	if err != nil {
		return fmt.Errorf("failed to lookup addresses: %w", err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("no addresses found for %s", r.host)
	}

	r.dnsEntries = addrs
	return nil
}
