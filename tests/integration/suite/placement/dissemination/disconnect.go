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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disconnect))
}

type disconnect struct {
	place *placement.Placement

	loglineLastDisconnected *logline.LogLine
}

func (n *disconnect) Setup(t *testing.T) []framework.Option {
	n.loglineLastDisconnected = logline.New(t, logline.WithStdoutLineContains(
		"handling last disconnected member in namespace ns1 , appid myapp1",
	))

	n.place = placement.New(t,
		placement.WithMetadataEnabled(true),
		placement.WithExecOptions(
			exec.WithStdout(n.loglineLastDisconnected.Stdout()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.loglineLastDisconnected, n.place),
	}
}

func (n *disconnect) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)

	t.Run("disconnecting stream for host that HAD actors removes actors from the placement table if no other host for the appid is present in the namespace", func(t *testing.T) {
		httpClient := client.HTTP(t)

		oldTableVersion := 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			oldTableVersion = n.place.CheckAPILevelInState(t, httpClient, 0)
		}, 5*time.Second, 10*time.Millisecond)

		host1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		t.Cleanup(func() {
			streamCancel()
		})
		stream1 := n.getStream(t, streamCtx)

		err := stream1.Send(host1)
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream1.Recv()
			if !assert.NoError(c, errR) {
				_ = stream1.CloseSend()
				return
			}

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Len(c, placementTables.GetTables().GetEntries(), 1)
				assert.Contains(c, placementTables.GetTables().GetEntries(), "actor1")
			}
		}, time.Second*15, time.Millisecond*10)

		tableVersion := n.place.CheckAPILevelInState(t, httpClient, 10)
		require.Greater(t, tableVersion, oldTableVersion)
		oldTableVersion = tableVersion

		// sidecar sends an update with empty entities
		emptyHost1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		err = stream1.Send(emptyHost1)
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream1.Recv()
			if !assert.NoError(c, errR) {
				_ = stream1.CloseSend()
				return
			}

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Empty(c, placementTables.GetTables().GetEntries())
			}
		}, time.Second*15, time.Millisecond*10)

		tableVersion = n.place.CheckAPILevelInState(t, httpClient, 10)
		require.Greater(t, tableVersion, oldTableVersion)
		oldTableVersion = tableVersion

		// sidecar is scaled down and it disconnects from the placement service
		_ = stream1.CloseSend()
		streamCancel()

		n.loglineLastDisconnected.EventuallyFoundAll(t)

		newTableVersion := n.place.CheckAPILevelInState(t, httpClient, 10)
		require.Equal(t, newTableVersion, oldTableVersion)
	})

	t.Run("disconnecting stream removes actors from the placement table if no other host for the appid is present in the namespace", func(t *testing.T) {
		hostMonitor := &v1pb.Host{
			Name:      "myappmonitor",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actormonitor"},
			Id:        "myappmonitor",
			ApiLevel:  uint32(20),
		}
		monitorCh := n.place.RegisterHost(t, ctx, hostMonitor)

		select {
		case <-ctx.Done():
			require.Fail(t, "context done")
		case placementTables := <-monitorCh:
			require.Len(t, placementTables.GetEntries(), 1)
			require.Contains(t, placementTables.GetEntries(), "actormonitor")

			entry, ok := placementTables.GetEntries()["actormonitor"]
			require.True(t, ok)
			loadMap := entry.GetLoadMap()
			require.Len(t, loadMap, 1)
			require.Contains(t, loadMap, hostMonitor.GetName())
		}

		host1 := &v1pb.Host{
			Name:      "myapp1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1"},
			Id:        "myapp1",
			ApiLevel:  uint32(20),
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		t.Cleanup(func() {
			streamCancel()
		})
		stream1 := n.getStream(t, streamCtx)

		err := stream1.Send(host1)
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream1.Recv()
			if !assert.NoError(c, errR) {
				_ = stream1.CloseSend()
				return
			}

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Len(c, placementTables.GetTables().GetEntries(), 2)
				assert.Contains(c, placementTables.GetTables().GetEntries(), "actor1")
				assert.Contains(c, placementTables.GetTables().GetEntries(), "actormonitor")
			}
		}, time.Second*15, time.Millisecond*10)

		// sidecar is scaled down and it disconnects from the placement service
		_ = stream1.CloseSend()
		streamCancel()

		// dissemination is done and monitorhost should receive an update
		msgCnt := 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-monitorCh:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				assert.Len(c, placementTables.GetEntries(), 1)
				assert.Contains(c, placementTables.GetEntries(), "actormonitor")
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)
	})

	t.Run("disconnecting stream does not remove actors from the placement table if other host for the appid is present in the namespace", func(t *testing.T) {
		hostMonitor := &v1pb.Host{
			Name:      "myappmonitor",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actormonitor"},
			Id:        "myappmonitor",
			ApiLevel:  uint32(20),
		}
		monitorCh := n.place.RegisterHost(t, ctx, hostMonitor)

		select {
		case <-ctx.Done():
			require.Fail(t, "context done")
		case placementTables := <-monitorCh:
			require.Len(t, placementTables.GetEntries(), 1)
			require.Contains(t, placementTables.GetEntries(), "actormonitor")

			entry, ok := placementTables.GetEntries()["actormonitor"]
			require.True(t, ok)
			loadMap := entry.GetLoadMap()
			require.Len(t, loadMap, 1)
			require.Contains(t, loadMap, hostMonitor.GetName())
		}

		host1App1 := &v1pb.Host{
			Name:      "myapp1-host1",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1"},
			Id:        "app1",
			ApiLevel:  uint32(20),
		}
		host1Ch := n.place.RegisterHost(t, ctx, host1App1)
		select {
		case <-ctx.Done():
			require.Fail(t, "context done")
		case placementTables := <-host1Ch:
			require.Len(t, placementTables.GetEntries(), 2)
			require.Contains(t, placementTables.GetEntries(), "actormonitor")
			require.Contains(t, placementTables.GetEntries(), "actor1")

			entry, ok := placementTables.GetEntries()["actor1"]
			require.True(t, ok)
			loadMap := entry.GetLoadMap()
			require.Len(t, loadMap, 1)
			require.Contains(t, loadMap, host1App1.GetName())
		}

		host2App1 := &v1pb.Host{
			Name:      "myapp1-host2",
			Namespace: "ns1",
			Port:      1231,
			Entities:  []string{"actor1"},
			Id:        "app1",
			ApiLevel:  uint32(20),
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		t.Cleanup(func() {
			streamCancel()
		})
		stream2 := n.getStream(t, streamCtx)

		err := stream2.Send(host2App1)
		require.NoError(t, err)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			placementTables, errR := stream2.Recv()
			if !assert.NoError(c, errR) {
				_ = stream2.CloseSend()
				return
			}

			if assert.Equal(c, "update", placementTables.GetOperation()) {
				assert.Len(c, placementTables.GetTables().GetEntries(), 2)
				assert.Contains(c, placementTables.GetTables().GetEntries(), "actor1")
				assert.Contains(c, placementTables.GetTables().GetEntries(), "actormonitor")
				entry, ok := placementTables.GetTables().GetEntries()["actor1"]
				require.True(t, ok)
				loadMap := entry.GetLoadMap()
				require.Len(t, loadMap, 2)
				require.Contains(t, loadMap, host1App1.GetName())
				require.Contains(t, loadMap, host2App1.GetName())
			}
		}, time.Second*15, time.Millisecond*10)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				require.Fail(t, "context done")
			case placementTables := <-host1Ch:
				require.Len(t, placementTables.GetEntries(), 2)
				require.Contains(t, placementTables.GetEntries(), "actormonitor")
				require.Contains(t, placementTables.GetEntries(), "actor1")

				entry, ok := placementTables.GetEntries()["actor1"]
				require.True(t, ok)
				loadMap := entry.GetLoadMap()
				require.Len(t, loadMap, 2)
				require.Contains(t, loadMap, host1App1.GetName())
				require.Contains(t, loadMap, host2App1.GetName())
			}
		}, time.Second*5, time.Millisecond*10)

		// sidecar for host2App1 is scaled down and it disconnects from the placement service
		_ = stream2.CloseSend()
		streamCancel()

		// dissemination is done and monitorhost should receive an update
		msgCnt := 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-monitorCh:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				require.Len(c, placementTables.GetEntries(), 2)
				require.Contains(c, placementTables.GetEntries(), "actormonitor")
				require.Contains(c, placementTables.GetEntries(), "actor1")
				entry, ok := placementTables.GetEntries()["actor1"]
				require.True(t, ok)
				loadMap := entry.GetLoadMap()
				require.Len(c, loadMap, 1)
				require.Contains(c, loadMap, host1App1.GetName())
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, 10*time.Second, 10*time.Millisecond)

		// host1App1 should receive an update
		msgCnt = 0
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			select {
			case <-ctx.Done():
				return
			case placementTables := <-host1Ch:
				if ctx.Err() != nil {
					return
				}

				msgCnt++
				require.Len(c, placementTables.GetEntries(), 2)
				require.Contains(c, placementTables.GetEntries(), "actormonitor")
				require.Contains(c, placementTables.GetEntries(), "actor1")
				entry, ok := placementTables.GetEntries()["actor1"]
				require.True(t, ok)
				loadMap := entry.GetLoadMap()
				require.Len(c, loadMap, 1)
				require.Contains(c, loadMap, host1App1.GetName())
			}
			assert.GreaterOrEqual(c, msgCnt, 1)
		}, time.Second*5, time.Millisecond*10)
	})
}

func (n *disconnect) getStream(t *testing.T, ctx context.Context) v1pb.Placement_ReportDaprStatusClient {
	t.Helper()

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, n.place.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := v1pb.NewPlacementClient(conn)

	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)

	return stream
}
