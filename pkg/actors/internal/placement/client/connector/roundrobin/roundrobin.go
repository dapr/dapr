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
	"net"

	"github.com/dapr/dapr/pkg/actors/internal/placement/client/connector"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.client.connector.roundrobin")

func NewDNSConnector(opts DNSOptions) (connector.Interface, error) {
	host, port, err := net.SplitHostPort(opts.Address)
	if err != nil {
		return nil, err
	}

	resolver := opts.resolver
	if opts.resolver == nil {
		resolver = &net.Resolver{PreferGo: true}
	}

	return &dnsroundrobin{
		host:     host,
		port:     port,
		gOpts:    opts.GRPCOptions,
		resolver: resolver,
	}, nil
}

func NewStaticConnector(opts StaticOptions) (connector.Interface, error) {
	return &staticroundrobin{
		addresses:    opts.Addresses,
		gOpts:        opts.GRPCOptions,
		addressIndex: -1,
	}, nil
}
