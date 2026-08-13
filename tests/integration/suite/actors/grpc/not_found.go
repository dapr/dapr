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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(notFound))
}

// notFound verifies that a codes.NotFound from OnActorInvoke is treated as a
// permanent error: daprd does not retry the invocation, so one client-side
// InvokeActor call results in exactly one OnActorInvoke callback. The app
// differentiates a "warmup" method (returns success) from "missing" (returns
// NotFound) so we can separate placement-readiness retries at the eventually
// layer from resiliency-driven retries at the transport layer.
type notFound struct {
	daprd *daprd.Daprd
	place *placement.Placement

	missingCalls atomic.Int32
}

func (n *notFound) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return n.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
			if r.GetMethod() == "missing" {
				n.missingCalls.Add(1)
				return nil, status.Error(codes.NotFound, "method not found")
			}
			return &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{}, nil
		}),
	)

	n.place = placement.New(t)
	n.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(n.place, n.daprd, srv),
	}
}

func (n *notFound) Run(t *testing.T, ctx context.Context) {
	n.place.WaitUntilRunning(t, ctx)
	n.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(n.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Warm up via a method that always succeeds — this absorbs the
	// placement-readiness retries without touching missingCalls.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "warmup",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	// Exactly one call to the NotFound method — resiliency must not retry
	// a permanent error.
	_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "a",
		Method:    "missing",
	})
	require.Error(t, invErr)
	assert.Equal(t, int32(1), n.missingCalls.Load(), "daprd must not retry after NotFound")

	// Guard against a late retry slipping in.
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(1), n.missingCalls.Load(), "no late retry of a NotFound method")
}
