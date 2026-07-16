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

package timeout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(closemidround))
}

type closemidround struct {
	place *placement.Placement
}

func (c *closemidround) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*10),
	)

	return []framework.Option{
		framework.WithProcesses(c.place),
	}
}

func (c *closemidround) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return c.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := c.place.Client(t, ctx)

	// Connect stream1 (healthy) and complete the first round.
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))

	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))

	// Connect stream2 (will disconnect mid-round).
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name: "app2", Port: 1002, Entities: []string{"actorA"},
		Id: "app2", Namespace: "default",
	}))

	// Both streams receive LOCK for the new round.
	resp1, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp1.GetOperation())

	resp2, err := stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp2.GetOperation())

	// Both respond to LOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name: "app2", Port: 1002, Entities: []string{"actorA"},
		Id: "app2", Namespace: "default",
	}))

	// Both receive UPDATE.
	resp1, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp1.GetOperation())

	resp2, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp2.GetOperation())

	// stream1 responds to UPDATE. stream2 disconnects mid-round.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))
	require.NoError(t, stream2.CloseSend())

	// stream1 should receive UNLOCK quickly (via advancePhase, not after the 10s
	// timeout). If advancePhase doesn't work, this would hang for 10 seconds and
	// the test would be slow.
	startTime := time.Now()
	resp1, err = stream1.Recv()
	elapsed := time.Since(startTime)
	require.NoError(t, err)
	require.Equal(t, "unlock", resp1.GetOperation())

	assert.Less(t, elapsed, 5*time.Second,
		"UNLOCK should arrive quickly via advancePhase, not after timeout")

	// Complete the round.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "app1", Port: 1001, Entities: []string{"actorA"},
		Id: "app1", Namespace: "default",
	}))

	// Placement table should show only app1.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		table := c.place.PlacementTables(t, ctx)
		if !assert.NotNil(col, table.Tables["default"]) {
			return
		}
		assert.Len(col, table.Tables["default"].Hosts, 1)
		if len(table.Tables["default"].Hosts) > 0 {
			assert.Equal(col, "app1", table.Tables["default"].Hosts[0].Name)
		}
	}, time.Second*10, time.Millisecond*10)
}
