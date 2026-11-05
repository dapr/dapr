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
	suite.Register(new(repeatedtablepush))
}

const (
	orderLock   = "lock"
	orderUpdate = "update"
	orderUnlock = "unlock"
)

// repeatedtablepush verifies that a sidecar with no actors which reports its
// host twice on the same stream does not wedge dissemination. A daprd that
// declares actor types but has no actor state store does this: it reports an
// empty host on connect, and again when its actor types are registered. Each
// report gets a one-shot table push. The acks of the second push must not
// complete a namespace-wide round and send an UNLOCK to the other streams,
// which they ignore, leaving the namespace stuck in UNLOCK.
type repeatedtablepush struct {
	place *placement.Placement
}

func (r *repeatedtablepush) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t,
		placement.WithDisseminateTimeout(time.Second*5),
	)

	return []framework.Option{
		framework.WithProcesses(r.place),
	}
}

func (r *repeatedtablepush) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)

	assert.Eventually(t, func() bool {
		return r.place.IsLeader(t, ctx)
	}, time.Second*10, time.Millisecond*10)

	client := r.place.Client(t, ctx)

	actorHost := &v1pb.Host{
		Name:      "app-with-actors",
		Port:      1234,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors",
		Namespace: "default",
	}
	noActorHost := &v1pb.Host{
		Name:      "app-no-actors",
		Port:      5678,
		Id:        "app-no-actors",
		Namespace: "default",
	}

	// ack answers an order the way daprd does: the operation and version of
	// the order it received.
	ack := func(stream v1pb.Placement_ReportDaprStatusClient, host *v1pb.Host, order *v1pb.PlacementOrder) {
		t.Helper()
		var op v1pb.HostOperation
		switch order.GetOperation() {
		case orderLock:
			op = v1pb.HostOperation_LOCK
		case orderUpdate:
			op = v1pb.HostOperation_UPDATE
		case orderUnlock:
			op = v1pb.HostOperation_UNLOCK
		default:
			require.Fail(t, "unexpected operation", order.GetOperation())
		}
		version := order.GetVersion()
		require.NoError(t, stream.Send(&v1pb.Host{
			Id:        host.GetId(),
			Namespace: host.GetNamespace(),
			Operation: op,
			Version:   &version,
		}))
	}

	// Stream1 has actors and completes a full round on its own.
	stream1, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(actorHost))
	for _, want := range []string{orderLock, orderUpdate, orderUnlock} {
		resp, rerr := stream1.Recv()
		require.NoError(t, rerr)
		require.Equal(t, want, resp.GetOperation())
		ack(stream1, actorHost, resp)
	}

	stream1Msgs := make(chan string, 10)
	go func() {
		for {
			resp, rerr := stream1.Recv()
			if rerr != nil {
				return
			}
			stream1Msgs <- resp.GetOperation()
		}
	}()

	// Stream2 has no actors and reports the same host twice. Each report gets
	// a one-shot table push, and stream2 acks every order it receives.
	stream2, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream2.Send(noActorHost))
	require.NoError(t, stream2.Send(noActorHost))
	for range 2 {
		for _, want := range []string{orderLock, orderUpdate, orderUnlock} {
			resp, rerr := stream2.Recv()
			require.NoError(t, rerr)
			require.Equal(t, want, resp.GetOperation())
			ack(stream2, noActorHost, resp)
		}
	}

	// Stream1 must not receive an UNLOCK for a round that never started.
	select {
	case op := <-stream1Msgs:
		require.Fail(t, "unexpected order on stream1", op)
	case <-time.After(2 * time.Second):
	}

	// A new host with actors must still start a round that reaches stream1.
	stream3, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err)
	require.NoError(t, stream3.Send(&v1pb.Host{
		Name:      "app-with-actors-2",
		Port:      4321,
		Entities:  []string{"actorA"},
		Id:        "app-with-actors-2",
		Namespace: "default",
	}))

	select {
	case op := <-stream1Msgs:
		require.Equal(t, orderLock, op)
	case <-time.After(10 * time.Second):
		require.Fail(t, "dissemination is stuck: stream1 received no LOCK for the new host")
	}
}
