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
	"sync/atomic"
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
	suite.Register(new(actorError))
}

// actorError verifies that when the app signals an actor-level error via
// SubscribeActorEventsRequestInvokeResponseAlpha1.error=true, the body payload round-trips to the
// caller (matching the HTTP contract: actor errors are returned as response
// data, not as a gRPC error — the X-Daprerrorresponseheader marker is what
// remote daprds use to detect the error class on cross-daprd calls).
type actorError struct {
	daprd *daprd.Daprd
	place *placement.Placement

	invocations atomic.Int32
}

func (a *actorError) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return a.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(context.Context, *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
			a.invocations.Add(1)
			return &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{
				Data:     []byte(`{"code":"INVALID","msg":"bad input"}`),
				Metadata: map[string]string{"content-type": "application/json"},
				Error:    true,
			}, nil
		}),
	)

	a.place = placement.New(t)
	a.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(a.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(a.place, a.daprd, srv),
	}
}

func (a *actorError) Run(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(a.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Daprd forwards the actor-error payload as the response Data — the
	// X-Daprerrorresponseheader marker is consumed by cross-daprd paths
	// (router.callRemoteActor) and does not surface as a gRPC error here.
	var resp *rtv1.InvokeActorResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var invErr error
		resp, invErr = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "explode",
		})
		// Actor ready iff the app was invoked at least once.
		assert.NoError(c, invErr)
		assert.GreaterOrEqual(c, a.invocations.Load(), int32(1))
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	require.NotNil(t, resp)
	// Error body must round-trip to the caller verbatim.
	assert.JSONEq(t, `{"code":"INVALID","msg":"bad input"}`, string(resp.GetData()))
}
