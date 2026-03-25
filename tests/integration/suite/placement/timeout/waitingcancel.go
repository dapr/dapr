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
	"strconv"
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
	suite.Register(new(waitingcancel))
}

type waitingcancel struct {
	place *placement.Placement
}

func (w *waitingcancel) Setup(t *testing.T) []framework.Option {
	w.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*2),
	)

	return []framework.Option{
		framework.WithProcesses(w.place),
	}
}

func (w *waitingcancel) Run(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return w.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := w.place.Client(t, ctx)

	// Stream1 connects and starts a dissemination round, but never responds past
	// LOCK (simulating a slow or crashing sidecar).
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&v1pb.Host{
		Name:      "blocker",
		Port:      1001,
		Entities:  []string{"actorA"},
		Id:        "blocker",
		Namespace: "default",
	}))

	// stream1 receives LOCK but does NOT respond.
	resp, err := stream1.Recv()
	require.NoError(t, err)
	require.Equal(t, "lock", resp.GetOperation())

	// While stream1 is blocking dissemination, connect 4 more replicas. These
	// should be queued in waitingToDisseminate.
	const numWaiting = 4
	waitingStreams := make([]v1pb.Placement_ReportDaprStatusClient, numWaiting)
	for i := range numWaiting {
		s, serr := client.ReportDaprStatus(ctx)
		require.NoError(t, serr)
		id := "waiter-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name:      id,
			Port:      int64(2001 + i),
			Entities:  []string{"actorA"},
			Id:        id,
			Namespace: "default",
		}))
		waitingStreams[i] = s
	}

	// Wait for the timeout to fire. It should disconnect stream1 AND cancel all
	// waiting connections.

	// stream1 should be disconnected with DeadlineExceeded.
	stream1ErrCh := make(chan error, 1)
	go func() {
		_, serr := stream1.Recv()
		stream1ErrCh <- serr
	}()

	select {
	case serr := <-stream1ErrCh:
		require.Error(t, serr)
		st, ok := status.FromError(serr)
		require.True(t, ok)
		assert.Equal(t, codes.DeadlineExceeded, st.Code())
	case <-time.After(10 * time.Second):
		require.Fail(t, "stream1 (blocker) was not disconnected by timeout")
	}

	// After the fix: waiting streams are NOT cancelled. They are added to the
	// new dissemination round started after the timeout. Each waiting stream
	// should receive LOCK from the new round. Complete the round by responding.
	for i, s := range waitingStreams {
		resp, rerr := s.Recv()
		require.NoError(t, rerr, "waiter-%d should receive LOCK (not be cancelled)", i)
		require.Equal(t, "lock", resp.GetOperation(), "waiter-%d should get lock", i)
	}

	// Respond to LOCK on all waiting streams.
	for i, s := range waitingStreams {
		id := "waiter-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name: id, Port: int64(2001 + i),
			Entities: []string{"actorA"}, Id: id, Namespace: "default",
		}))
	}

	// All should receive UPDATE.
	for i, s := range waitingStreams {
		resp, rerr := s.Recv()
		require.NoError(t, rerr, "waiter-%d update", i)
		require.Equal(t, "update", resp.GetOperation())
	}

	for i, s := range waitingStreams {
		id := "waiter-" + strconv.Itoa(i)
		require.NoError(t, s.Send(&v1pb.Host{
			Name: id, Port: int64(2001 + i),
			Entities: []string{"actorA"}, Id: id, Namespace: "default",
		}))
	}

	// All should receive UNLOCK.
	for i, s := range waitingStreams {
		resp, rerr := s.Recv()
		require.NoError(t, rerr, "waiter-%d unlock", i)
		require.Equal(t, "unlock", resp.GetOperation())
	}

	// Verify placement table contains all 4 waiting apps (blocker is gone).
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		table := w.place.PlacementTables(t, ctx)
		if !assert.NotNil(c, table.Tables["default"]) {
			return
		}
		assert.Len(c, table.Tables["default"].Hosts, numWaiting)
	}, time.Second*10, time.Millisecond*100)
}
