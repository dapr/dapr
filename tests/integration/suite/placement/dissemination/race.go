/*
Copyright 2024 The Dapr Authors
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

package dissemination

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(race))
}

type race struct {
	place *placement.Placement
}

func (r *race) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(r.place),
	}
}

func (r *race) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)

	for i := range 10 {
		host := &v1pb.Host{
			Name:      fmt.Sprintf("inithost%d", i),
			Namespace: fmt.Sprintf("initns%d", i),
			Port:      1231,
			Entities:  []string{fmt.Sprintf("initactor%d", i)},
			Id:        fmt.Sprintf("initapp%d", i),
			ApiLevel:  uint32(20),
		}
		_ = r.place.RegisterHost(t, ctx, host)
	}

	t.Run("register and unregister in loop", func(t *testing.T) {
		host := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "appns",
			Port:      1231,
			Entities:  []string{"appactor1"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		emptyHost := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "appns",
			Port:      1231,
			Entities:  []string{},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		// wait for placement to be ready and register the initial host
		stream := r.waitForPlacementReady(t, ctx, host)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream.Recv()
			if !assert.NoError(c, errR) {
				_ = stream.CloseSend()
				return
			}

			t.Logf("received operation: %s", placementTables.GetOperation())

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Len(c, placementTables.GetTables().GetEntries(), 1)
				assert.Contains(c, placementTables.GetTables().GetEntries(), "appactor1")
			}
		}, time.Second*15, time.Millisecond*10)

		tableUpdates := r.collectPlacementTableUpdates(t, ctx, stream)

		time.Sleep(5 * time.Second)
		tableUpdates.mu.RLock()
		require.Empty(t, tableUpdates.tables)
		tableUpdates.mu.RUnlock()

		for range 50 {
			require.NoError(t, stream.Send(emptyHost))
			time.Sleep(50 * time.Millisecond)
			require.NoError(t, stream.Send(host))
		}

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			tableUpdates.mu.RLock()
			defer tableUpdates.mu.RUnlock()
			assert.GreaterOrEqual(c, len(tableUpdates.tables), 1)
		}, time.Second*10, time.Millisecond*10)

		// check last table update
		tableUpdates.mu.RLock()
		lastTableUpdate := tableUpdates.tables[len(tableUpdates.tables)-1]
		tableUpdates.mu.RUnlock()
		require.Len(t, lastTableUpdate.GetEntries(), 1)
		require.Contains(t, lastTableUpdate.GetEntries(), "appactor1")
	})
}

func (r *race) waitForPlacementReady(t *testing.T, ctx context.Context, initialHost *v1pb.Host) v1pb.Placement_ReportDaprStatusClient {
	t.Helper()

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, r.place.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := v1pb.NewPlacementClient(conn)

	var stream v1pb.Placement_ReportDaprStatusClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err = client.ReportDaprStatus(ctx)
		if !assert.NoError(c, err) {
			return
		}

		if !assert.NoError(c, stream.Send(initialHost)) {
			_ = stream.CloseSend()
			return
		}

		_, err = stream.Recv()
		if !assert.NoError(c, err) {
			_ = stream.CloseSend()
			return
		}
		t.Cleanup(func() {
			_ = stream.CloseSend()
		})
	}, time.Second*15, time.Millisecond*10)

	return stream
}

type placementTablesUpdates struct {
	mu     sync.RWMutex
	tables []*v1pb.PlacementTables
}

func (r *race) collectPlacementTableUpdates(t *testing.T, ctx context.Context, stream v1pb.Placement_ReportDaprStatusClient) *placementTablesUpdates {
	updates := &placementTablesUpdates{}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			in, err := stream.Recv()
			if err != nil {
				return
			}

			if in.GetOperation() == "update" {
				tables := in.GetTables()
				assert.NotEmptyf(t, tables, "Placement table is empty")

				updates.mu.Lock()
				updates.tables = append(updates.tables, tables)
				updates.mu.Unlock()
			}
		}
	}()

	return updates
}
