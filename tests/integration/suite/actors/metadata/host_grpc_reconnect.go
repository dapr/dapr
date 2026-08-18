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

package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hostGRPCReconnect))
}

// hostGRPCReconnect verifies daprd's lifecycle handling for the actor
// callback stream end-to-end: connect, observe state, disconnect,
// observe cleared state, reconnect, observe restored state. Multi-connection
// snapshot/routing semantics are covered by the unit tests in
// pkg/actors/callbackstream — this test exercises the gRPC API surface
// (SubscribeActorEventsAlpha1 ↔ daprd metadata) for the
// pod-restart-style scenario that real apps trigger.
type hostGRPCReconnect struct {
	daprd *daprd.Daprd
	place *placement.Placement
	app   *procgrpcapp.App
}

func (m *hostGRPCReconnect) Setup(t *testing.T) []framework.Option {
	// procgrpcapp here only exists to give daprd a TCP port for
	// blockUntilAppIsReady — it does not open the actor stream itself.
	// The test goroutine drives all streams directly so disconnect timing
	// is deterministic.
	m.app = procgrpcapp.New(t)

	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(m.app.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(m.place, m.daprd, m.app),
	}
}

func (m *hostGRPCReconnect) Run(t *testing.T, ctx context.Context) {
	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(m.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := rtv1.NewDaprClient(conn)

	// Phase 1: app opens the stream and registers actorA. Daprd should
	// reflect that registration in its metadata.
	streamA, cancelA := openActorStream(t, ctx, client, "actorA")

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		res := m.daprd.GetMetadata(c, ctx)
		assert.True(c, res.ActorRuntime.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{
			{Type: "actorA"},
		}, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 50*time.Millisecond, "stream registration not visible in metadata")

	// Phase 2: stream disconnects. The deferred UnRegisterHosted in the
	// API handler removes the actor type from daprd's view. Verify
	// metadata reflects the disconnect.
	cancelA()
	require.NoError(t, streamA.CloseSend())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		res := m.daprd.GetMetadata(c, ctx)
		assert.Empty(c, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 50*time.Millisecond, "metadata still reports active actors after stream disconnect")

	// Phase 3: app reconnects. Daprd must pick the registration up again
	// without a restart. This is the real-world pod-restart path.
	streamB, cancelB := openActorStream(t, ctx, client, "actorA")
	t.Cleanup(cancelB)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		res := m.daprd.GetMetadata(c, ctx)
		assert.True(c, res.ActorRuntime.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{
			{Type: "actorA"},
		}, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 50*time.Millisecond, "daprd did not re-register actor type after reconnect")

	_ = streamB // Cleanup closes it.
}

// openActorStream opens a SubscribeActorEventsAlpha1 stream to daprd,
// sends the initial registration with the supplied entity, waits for the
// InitialResponse ack, and returns the stream plus a cancel function
// that tears it down. The recv loop is drained in a background goroutine
// so the stream stays open while the test inspects daprd state — daprd
// won't push callback messages here (no invocations) but disconnect-time
// notifications still need to be consumed.
func openActorStream(
	t *testing.T,
	parent context.Context,
	client rtv1.DaprClient,
	entity string,
) (rtv1.Dapr_SubscribeActorEventsAlpha1Client, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)

	stream, err := client.SubscribeActorEventsAlpha1(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&rtv1.SubscribeActorEventsRequestAlpha1{
		RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities: []string{entity},
			},
		},
	}))

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, resp.GetInitialResponse(),
		"expected InitialResponse ack, got %T", resp.GetResponseType())

	go func() {
		for {
			if _, recvErr := stream.Recv(); recvErr != nil {
				return
			}
		}
	}()

	return stream, cancel
}
