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
	"strconv"
	"sync"
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
	suite.Register(new(batchconnect))
}

type batchconnect struct {
	place *placement.Placement
}

func (b *batchconnect) Setup(t *testing.T) []framework.Option {
	b.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(b.place),
	}
}

func (b *batchconnect) Run(t *testing.T, ctx context.Context) {
	b.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return b.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := b.place.Client(t, ctx)

	// Connect stream1 first to start a dissemination round.
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))

	// Receive LOCK.
	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())
	lockVersion := resp.GetVersion()

	// While dissemination is in progress (stream1 is in LOCK), connect 3
	// more sidecars. They should be queued in waitingToDisseminate.
	const numWaiting = 3
	streams := make([]v1pb.Placement_ReportDaprStatusClient, numWaiting)
	for i := range numWaiting {
		s, serr := client.ReportDaprStatus(ctx)
		require.NoError(t, serr)
		id := "app" + strconv.Itoa(i+2)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(1002 + i),
			Entities:  []string{"actorA"},
			Id:        id,
			Namespace: "default",
		}))
		streams[i] = s
	}

	// Complete the first dissemination round for stream1.
	// Send report to advance to UPDATE.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "update", resp.GetOperation())

	// Send report to advance to UNLOCK.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))
	resp, err = stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "unlock", resp.GetOperation())
	require.Equal(t, lockVersion, resp.GetVersion())

	// Acknowledge the UNLOCK so the server completes round 1.
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "app1",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "app1",
		Namespace: "default",
	}))

	// After unlock, the waiting connections should be batched and processed. All
	// 4 streams (stream1 + 3 waiting) should receive LOCK at the same new
	// version, indicating a single batched dissemination round.
	type lockResult struct {
		version uint64
		err     error
	}

	results := make([]lockResult, numWaiting+1)
	var wg sync.WaitGroup

	// Collect LOCK from stream1.
	wg.Go(func() {
		r, rerr := stream1.Recv()
		if rerr != nil {
			results[0] = lockResult{err: rerr}
			return
		}
		results[0] = lockResult{version: r.GetVersion()}
	})

	// Collect LOCK from waiting streams.
	for i, s := range streams {
		wg.Go(func() {
			r, rerr := s.Recv()
			if rerr != nil {
				results[i+1] = lockResult{err: rerr}
				return
			}
			results[i+1] = lockResult{version: r.GetVersion()}
		})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		require.Fail(t, "timed out waiting for LOCK on all streams")
	}

	// All streams should have received LOCK at the same version.
	for i, r := range results {
		require.NoError(t, r.err, "stream %d error", i)
	}

	batchVersion := results[0].version
	require.Greater(t, batchVersion, lockVersion,
		"batch version should be higher than the first round version")

	for i, r := range results[1:] {
		assert.Equal(t, batchVersion, r.version,
			"stream %d should receive same batch version", i+2)
	}

	// Complete the batched round: all streams report to advance through UPDATE
	// and UNLOCK.
	allStreams := append([]v1pb.Placement_ReportDaprStatusClient{stream1}, streams...)
	for i, s := range allStreams {
		id := "app" + strconv.Itoa(i+1)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(1001 + i),
			Entities:  []string{"actorA"},
			Id:        id,
			Namespace: "default",
		}))
	}

	// All should get UPDATE.
	for i, s := range allStreams {
		r, rerr := s.Recv()
		require.NoError(t, rerr, "stream %d update", i)
		require.Equal(t, "update", r.GetOperation())
	}

	for i, s := range allStreams {
		id := "app" + strconv.Itoa(i+1)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(1001 + i),
			Entities:  []string{"actorA"},
			Id:        id,
			Namespace: "default",
		}))
	}

	// All should get UNLOCK at the batch version.
	for i, s := range allStreams {
		r, rerr := s.Recv()
		require.NoError(t, rerr, "stream %d unlock", i)
		require.Equal(t, "unlock", r.GetOperation())
		assert.Equal(t, batchVersion, r.GetVersion(),
			"stream %d unlock version", i)
	}

	// Verify all 4 hosts are in the placement table.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := b.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, numWaiting+1)
	}, time.Second*10, time.Millisecond*10)
}
