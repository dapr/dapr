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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

// Placement is a scripted in-process placement gRPC server: each Host report
// gets a one-shot LOCK/UPDATE/UNLOCK round containing just that host. Config
// returns Unimplemented unless WithDisseminateTimeout is set.
type Placement struct {
	*procgrpc.GRPC

	disseminateTimeout time.Duration
	configCalled       atomic.Int64
	version            atomic.Uint64
	roundsCompleted    atomic.Int64
}

func New(t *testing.T, fopts ...Option) *Placement {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	p := &Placement{
		disseminateTimeout: opts.disseminateTimeout,
	}

	p.GRPC = procgrpc.New(t, procgrpc.WithRegister(func(s *grpc.Server) {
		v1pb.RegisterPlacementServer(s, &server{p: p})
	}))

	return p
}

// ConfigCalled returns how many times the Config RPC has been invoked.
func (p *Placement) ConfigCalled() int64 {
	return p.configCalled.Load()
}

// RoundsCompleted returns how many dissemination rounds have been sent.
func (p *Placement) RoundsCompleted() int64 {
	return p.roundsCompleted.Load()
}

type server struct {
	p *Placement
}

func (s *server) Config(_ context.Context, _ *v1pb.ConfigRequest) (*v1pb.ConfigResponse, error) {
	s.p.configCalled.Add(1)
	if s.p.disseminateTimeout <= 0 {
		return nil, status.Error(codes.Unimplemented, "unknown method Config")
	}
	return &v1pb.ConfigResponse{
		DisseminateTimeout: durationpb.New(s.p.disseminateTimeout),
	}, nil
}

func (s *server) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	for {
		host, err := stream.Recv()
		if err != nil {
			return err
		}

		// Drain acknowledgements for in-flight rounds.
		if host.GetOperation() != v1pb.HostOperation_REPORT {
			continue
		}

		version := s.p.version.Add(1)

		tables := &v1pb.PlacementTables{
			ReplicationFactor: 100,
			Entries:           make(map[string]*v1pb.PlacementTable, len(host.GetEntities())),
		}
		for _, entity := range host.GetEntities() {
			tables.Entries[entity] = &v1pb.PlacementTable{
				LoadMap: map[string]*v1pb.Host{
					host.GetName(): {
						Name: host.GetName(),
						Id:   host.GetId(),
						Port: host.GetPort(),
					},
				},
			}
		}

		if err := stream.Send(&v1pb.PlacementOrder{Operation: "lock", Version: version}); err != nil {
			return err
		}
		if err := stream.Send(&v1pb.PlacementOrder{Operation: "update", Version: version, Tables: tables}); err != nil {
			return err
		}
		if err := stream.Send(&v1pb.PlacementOrder{Operation: "unlock", Version: version}); err != nil {
			return err
		}

		s.p.roundsCompleted.Add(1)
	}
}
