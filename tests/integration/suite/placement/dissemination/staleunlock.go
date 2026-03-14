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

package dissemination

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
	suite.Register(new(staleunlock))
}

// staleunlock verifies that a sidecar receiving LOCK -> UPDATE -> UNLOCK with
// a stale (lower) version correctly ignores the UNLOCK and preserves the
// current version. This exercises the version comparison guard in the UNLOCK
// handler of the sidecar disseminator.
type staleunlock struct {
	place *placement.Placement
}

func (s *staleunlock) Setup(t *testing.T) []framework.Option {
	s.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(s.place),
	}
}

func (s *staleunlock) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return s.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := s.place.Client(t, ctx)

	// Connect stream1 - a sidecar with actors.
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))

	// Receive LOCK for stream1.
	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())
	version1 := resp.GetVersion()
	require.Positive(t, version1)

	// Report back to advance to UPDATE.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	// Report back to advance to UNLOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	unlockVersion := resp.GetVersion()

	// Acknowledge the UNLOCK so the server completes round 1.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))

	// Now connect stream2 which triggers a new dissemination round at a higher
	// version.
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(&v1pb.Host{
		Name:      "app2",
		Port:      5678,
		Entities:  []string{"actorB"},
		Id:        "app2",
		Namespace: "default",
	}))

	// Both streams receive LOCK at a new (higher) version.
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())
	version2 := resp.GetVersion()
	require.Greater(t, version2, unlockVersion,
		"new dissemination round should have a higher version")

	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	// Complete the second round for both streams: UPDATE then UNLOCK.
	for _, stream := range []v1pb.Placement_ReportDaprStatusClient{stream1, stream2} {
		require.NoError(t, stream.Send(&v1pb.Host{
			Name:      "app1",
			Port:      1234,
			Entities:  []string{"actorA"},
			Id:        "app1",
			Namespace: "default",
		}))
	}

	// Both should receive UPDATE.
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())
	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	for _, stream := range []v1pb.Placement_ReportDaprStatusClient{stream1, stream2} {
		require.NoError(t, stream.Send(&v1pb.Host{
			Name:      "app1",
			Port:      1234,
			Entities:  []string{"actorA"},
			Id:        "app1",
			Namespace: "default",
		}))
	}

	// Both should receive UNLOCK at version2.
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	require.Equal(t, version2, resp.GetVersion())

	resp, err = stream2.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	require.Equal(t, version2, resp.GetVersion())

	// Verify placement tables show both hosts.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := s.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, 2)
		assert.Equal(c, version2, table.Tables["default"].Version)
	}, time.Second*10, time.Millisecond*10)
}
