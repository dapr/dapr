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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

// Placement is a scripted fake Placement server which sends lock, update,
// and unlock orders exactly when the test tells it to, so tests can produce
// order sequences the real Placement server only emits under churn.
type Placement struct {
	*procgrpc.GRPC
	v1pb.UnimplementedPlacementServer

	registered chan *v1pb.Host
	orders     chan *v1pb.PlacementOrder
}

func New(t *testing.T) *Placement {
	t.Helper()

	p := &Placement{
		registered: make(chan *v1pb.Host, 1),
		orders:     make(chan *v1pb.PlacementOrder),
	}
	p.GRPC = procgrpc.New(t, procgrpc.WithRegister(func(s *grpc.Server) {
		v1pb.RegisterPlacementServer(s, p)
	}))

	return p
}

func (p *Placement) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	recvDone := make(chan struct{})

	go func() {
		defer close(recvDone)
		// Only the first Host message of each stream is surfaced as a
		// registration, so tests can observe the daprd reconnecting with a
		// fresh stream. Later messages are heartbeats, which are drained.
		first := true
		for {
			host, err := stream.Recv()
			if err != nil {
				return
			}
			if !first {
				continue
			}
			first = false
			select {
			case p.registered <- host:
			default:
			}
		}
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-recvDone:
			return nil
		case order := <-p.orders:
			if err := stream.Send(order); err != nil {
				return err
			}
		}
	}
}

// WaitForRegistration returns the first Host report received on the next
// daprd stream connecting to this fake Placement server. Call it again after
// forcing a stream reset to observe the reconnection.
func (p *Placement) WaitForRegistration(t *testing.T, ctx context.Context) *v1pb.Host {
	t.Helper()
	select {
	case host := <-p.registered:
		return host
	case <-ctx.Done():
		require.Fail(t, "daprd did not register with placement in time")
		return nil
	case <-time.After(time.Second * 20):
		require.Fail(t, "daprd did not register with placement in time")
		return nil
	}
}

// SendOrder sends a placement order to the connected daprd stream.
func (p *Placement) SendOrder(t *testing.T, ctx context.Context, operation string, tables *v1pb.PlacementTables) {
	t.Helper()
	select {
	case p.orders <- &v1pb.PlacementOrder{Operation: operation, Tables: tables}:
	case <-ctx.Done():
		require.Fail(t, "failed to send placement order in time")
	case <-time.After(time.Second * 20):
		require.Fail(t, "failed to send placement order in time")
	}
}

// TablesWithHost returns placement tables which place every given actor type
// on the given host.
func TablesWithHost(host *v1pb.Host, version string, actorTypes ...string) *v1pb.PlacementTables {
	entries := make(map[string]*v1pb.PlacementTable, len(actorTypes))
	for _, actorType := range actorTypes {
		entries[actorType] = &v1pb.PlacementTable{
			LoadMap: map[string]*v1pb.Host{
				host.GetName(): host,
			},
		}
	}

	return &v1pb.PlacementTables{
		Version:           version,
		Entries:           entries,
		ReplicationFactor: 100,
	}
}
