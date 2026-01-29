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

package static

import (
	"context"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.manager.connector.static")

type staticConnector struct {
	addresses    []string
	addressIndex int

	gOpts []grpc.DialOption
}

type Options struct {
	GRPCOptions []grpc.DialOption
	Addresses   []string
}

func New(opts Options) (connector.Interface, error) {
	return &staticConnector{
		addresses:    opts.Addresses,
		gOpts:        opts.GRPCOptions,
		addressIndex: -1,
	}, nil
}

func (r *staticConnector) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	r.addressIndex = (r.addressIndex + 1) % len(r.addresses)

	address := r.Address()
	log.Debugf("Attempting to connect to placement %s", address)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, address, r.gOpts...)
	if err != nil {
		return nil, err
	}

	log.Infof("Connected to placement %s", address)

	return conn, nil
}

func (r *staticConnector) Address() string {
	return r.addresses[r.addressIndex]
}
