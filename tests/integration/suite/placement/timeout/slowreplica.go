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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(slowreplica))
}

type slowreplica struct {
	place *placement.Placement
}

func (s *slowreplica) Setup(t *testing.T) []framework.Option {
	s.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	return []framework.Option{
		framework.WithProcesses(s.place),
	}
}

func (s *slowreplica) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return s.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := s.place.Client(t, ctx)

	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "healthy-app",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "healthy-app",
		Namespace: "default",
	}))

	// stream1 receives LOCK for version 1.
	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())
	version1 := resp.GetVersion()

	// stream1 responds to advance to UPDATE.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "healthy-app",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "healthy-app",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	// stream1 responds to advance to UNLOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "healthy-app",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "healthy-app",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	require.Equal(t, version1, resp.GetVersion())

	// stream1 acknowledges UNLOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "healthy-app",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "healthy-app",
		Namespace: "default",
	}))

	// Now connect stream2 (the "slow" replica) which triggers a new
	// dissemination round.
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name:      "slow-app",
		Port:      1002,
		Entities:  []string{"actorA"},
		Id:        "slow-app",
		Namespace: "default",
	}))

	// Both streams should receive LOCK for version 2.
	resp1, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp1.GetOperation())
	version2 := resp1.GetVersion()
	require.Greater(t, version2, version1)

	resp2, err := stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp2.GetOperation())
	require.Equal(t, version2, resp2.GetVersion())

	// stream1 (healthy) responds immediately.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "healthy-app",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "healthy-app",
		Namespace: "default",
	}))

	// stream2 (slow) does NOT respond. It hangs in the LOCK phase.
	// The dissemination timeout will fire after 2 seconds.

	// Collect the error from stream2.
	stream2ErrCh := make(chan error, 1)
	go func() {
		_, serr := stream2.Recv()
		stream2ErrCh <- serr
	}()

	// stream2 (the slow one) MUST be disconnected with DeadlineExceeded.
	select {
	case serr := <-stream2ErrCh:
		require.Error(t, serr)
		st, ok := status.FromError(serr)
		require.True(t, ok)
		assert.Equal(t, codes.DeadlineExceeded, st.Code())
	case <-time.After(10 * time.Second):
		require.Fail(t, "stream2 (slow) was not disconnected")
	}

	// stream1 (healthy) should survive the timeout. The server starts a new
	// dissemination round with only stream1. Complete the round to prove
	// stream1 is still alive and functional.
	resp1, err = stream1.Recv()
	require.NoError(t, err, "stream1 should receive new LOCK after timeout kills stream2")
	require.Equal(t, "lock", resp1.GetOperation())

	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "healthy-app", Port: 1001,
		Entities: []string{"actorA"}, Id: "healthy-app", Namespace: "default",
	}))
	resp1, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp1.GetOperation())

	require.NoError(t, stream1.Send(&v1pb.Host{
		Name: "healthy-app", Port: 1001,
		Entities: []string{"actorA"}, Id: "healthy-app", Namespace: "default",
	}))
	resp1, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp1.GetOperation())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 1)
		if len(table.Tables["default"].Hosts) > 0 {
			assert.Equal(c, "healthy-app", table.Tables["default"].Hosts[0].Name)
		}
	}, time.Second*5, time.Millisecond*100)
}
