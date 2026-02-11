/*
Copyright 2026 The Dapr Authors
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
	"time"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/internal/leadership"
	"github.com/dapr/dapr/pkg/placement/internal/server"
	"github.com/dapr/dapr/pkg/placement/peers"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
)

type Options struct {
	NodeID            string
	Port              int
	ListenAddress     string
	Security          security.Handler
	Healthz           healthz.Healthz
	KeepAliveTime     time.Duration
	KeepAliveTimeout  time.Duration
	ReplicationFactor int64

	DisseminateTimeout time.Duration

	Peers []peers.PeerInfo
}

type Placement struct {
	server     *server.Server
	leadership *leadership.Leadership
}

func New(opts Options) (*Placement, error) {
	leadership, err := leadership.New(leadership.Options{
		ID:       opts.NodeID,
		Peers:    opts.Peers,
		Healthz:  opts.Healthz,
		Security: opts.Security,
	})
	if err != nil {
		return nil, err
	}

	server := server.New(server.Options{
		NodeID:             opts.NodeID,
		Port:               opts.Port,
		ListenAddress:      opts.ListenAddress,
		Leadership:         leadership,
		Security:           opts.Security,
		Healthz:            opts.Healthz,
		KeepAliveTime:      opts.KeepAliveTime,
		KeepAliveTimeout:   opts.KeepAliveTimeout,
		ReplicationFactor:  opts.ReplicationFactor,
		DisseminateTimeout: opts.DisseminateTimeout,
	})

	return &Placement{
		server:     server,
		leadership: leadership,
	}, nil
}

func (p *Placement) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		p.leadership.Run,
		p.server.Run,
	).Run(ctx)
}

func (p *Placement) StatePlacementTables(ctx context.Context) (*v1pb.StatePlacementTables, error) {
	return p.server.StatePlacementTables(ctx)
}
