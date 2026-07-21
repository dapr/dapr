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

package grpc

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
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(noappport))
}

// noappport validates the headline property of the actor callback stream:
// the application does not need to listen on any port. daprd is started
// with no --app-port and no app process exists anywhere in this test. The
// test itself plays the application: it dials daprd as a plain gRPC
// client, registers an actor type over SubscribeActorEventsAlpha1, and
// serves an actor invocation over that same stream.
type noappport struct {
	daprd *daprd.Daprd
	place *placement.Placement
}

func (n *noappport) Setup(t *testing.T) []framework.Option {
	n.place = placement.New(t)
	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("grpc"),
		// Deliberately no WithAppPort: the app never listens anywhere.
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(n.place, n.daprd),
	}
}

func (n *noappport) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(n.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	stream, err := client.SubscribeActorEventsAlpha1(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&rtv1.SubscribeActorEventsRequestAlpha1{
		RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InitialRequest{
			InitialRequest: &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities: []string{"myactortype"},
			},
		},
	}))

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, resp.GetInitialResponse(),
		"expected InitialResponse ack, got %T", resp.GetResponseType())

	streamErr := make(chan error, 1)
	go func() {
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				streamErr <- recvErr
				return
			}
			req := msg.GetInvokeRequest()
			if req == nil {
				continue
			}
			sendErr := stream.Send(&rtv1.SubscribeActorEventsRequestAlpha1{
				RequestType: &rtv1.SubscribeActorEventsRequestAlpha1_InvokeResponse{
					InvokeResponse: &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{
						Id:   req.GetId(),
						Data: append([]byte("echo:"), req.GetData()...),
					},
				},
			})
			if sendErr != nil {
				streamErr <- sendErr
				return
			}
		}
	}()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		res := n.daprd.GetMetadata(c, ctx)
		assert.True(c, res.ActorRuntime.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{
			{Type: "myactortype"},
		}, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 10*time.Millisecond,
		"stream registration not visible in metadata")

	var invResp *rtv1.InvokeActorResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var invErr error
		invResp, invErr = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a1",
			Method:    "ping",
			Data:      []byte("hello"),
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	require.NotNil(t, invResp)
	assert.Equal(t, []byte("echo:hello"), invResp.GetData(),
		"invocation must round-trip through the app's outbound stream")

	select {
	case sErr := <-streamErr:
		require.FailNow(t, "actor callback stream failed", "%v", sErr)
	default:
	}
}
