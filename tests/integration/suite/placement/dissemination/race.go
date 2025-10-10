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

package dissemination

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		metrics := r.place.Metrics(c, ctx)
		assert.Len(c, metrics.MatchMetric("dapr_placement_leader_status"), 1)
		assert.Len(c, metrics.MatchMetric("dapr_placement_raft_leader_status"), 1)
	}, time.Second*15, time.Millisecond*10)

	for i := range 1100 {
		host := &v1pb.Host{
			Name:      fmt.Sprintf("inithost%d", i),
			Namespace: fmt.Sprintf("initns%d", i),
			Port:      1231,
			Entities:  []string{fmt.Sprintf("initactor%d", i)},
			Id:        fmt.Sprintf("initapp%d", i),
			ApiLevel:  uint32(20),
		}
		r.simulateHost(t, ctx, host)
	}
	time.Sleep(3 * time.Second)

	t.Run("register mode hosts", func(t *testing.T) {
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
		stream := r.reportDaprStatusAndWaitForAck(t, ctx, host)

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

		time.Sleep(1 * time.Second)
		require.NoError(t, stream.Send(emptyHost))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream.Recv()
			if !assert.NoError(c, errR) {
				_ = stream.CloseSend()
				return
			}

			t.Logf("received operation: %s", placementTables.GetOperation())

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Empty(c, placementTables.GetTables().GetEntries())
			}
		}, time.Second*15, time.Millisecond*10)

		time.Sleep(1 * time.Second)
		require.NoError(t, stream.Send(host))

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
	})
}

func (r *race) getPlacementClient(t *testing.T, ctx context.Context) v1pb.PlacementClient {
	t.Helper()

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, r.place.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := v1pb.NewPlacementClient(conn)
	return client
}

func (r *race) reportDaprStatusAndWaitForAck(t *testing.T, ctx context.Context, initialHost *v1pb.Host) v1pb.Placement_ReportDaprStatusClient {
	t.Helper()
	client := r.getPlacementClient(t, ctx)

	var stream v1pb.Placement_ReportDaprStatusClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
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

func (r *race) simulateHost(t *testing.T, ctx context.Context, host *v1pb.Host) {
	pclient := r.getPlacementClient(t, ctx)
	doneCh := make(chan error)
	streamCtx, streamCancel := context.WithCancel(ctx)

	t.Cleanup(func() {
		streamCancel()
		select {
		case err := <-doneCh:
			if !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
				require.NoError(t, err)
			}
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout waiting for stream to close")
		}
	})
	go func(pclient v1pb.PlacementClient) {
		stream, err := pclient.ReportDaprStatus(streamCtx)
		mu := sync.Mutex{}
		if err != nil {
			doneCh <- err
			return
		}
		err = stream.Send(host)
		if err != nil {
			doneCh <- err
			return
		}
		_, err = stream.Recv()
		if err != nil {
			doneCh <- err
			return
		}

		// Send dapr status messages every second
		go func(stream v1pb.Placement_ReportDaprStatusClient) {
			for {
				select {
				case <-streamCtx.Done():
					return
				case <-time.After(1 * time.Second):
					mu.Lock()
					err := stream.Send(host)
					mu.Unlock()
					if err != nil {
						doneCh <- err
						return
					}
				}
			}
		}(stream)
		<-streamCtx.Done()
		mu.Lock()
		defer mu.Unlock()
		doneCh <- stream.CloseSend()
	}(pclient)
}
