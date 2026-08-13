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
	"sync"
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
	suite.Register(new(contentRoundTrip))
}

// contentRoundTrip verifies that request data, request metadata
// (content-type), response data, and response content-type all traverse the
// gRPC transport unchanged.
type contentRoundTrip struct {
	daprd *daprd.Daprd
	place *placement.Placement

	mu       sync.Mutex
	seenData []byte
	seenCT   string
}

func (c *contentRoundTrip) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return c.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
			c.mu.Lock()
			c.seenData = append([]byte(nil), req.GetData()...)
			c.seenCT = req.GetMetadata()["content-type"]
			c.mu.Unlock()
			return &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{
				Data:     []byte(`{"pong":true}`),
				Metadata: map[string]string{"content-type": "application/json"},
			}, nil
		}),
	)

	c.place = placement.New(t)
	c.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(c.place, c.daprd, srv),
	}
}

func (c *contentRoundTrip) Run(t *testing.T, ctx context.Context) {
	c.place.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(c.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	var resp *rtv1.InvokeActorResponse
	require.EventuallyWithT(t, func(cc *assert.CollectT) {
		var invErr error
		resp, invErr = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "ping",
			Data:      []byte(`{"ping":1}`),
			Metadata:  map[string]string{"content-type": "application/json"},
		})
		assert.NoError(cc, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	require.NotNil(t, resp)
	assert.Equal(t, []byte(`{"pong":true}`), resp.GetData())

	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Equal(t, []byte(`{"ping":1}`), c.seenData, "request body must round-trip unchanged")
	assert.Equal(t, "application/json", c.seenCT, "request content-type must be forwarded to the app")
}
