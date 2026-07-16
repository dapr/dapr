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
	suite.Register(new(reentrancy))
}

// reentrancy verifies the gRPC transport forwards the Dapr-Reentrancy-Id
// header as gRPC metadata when reentrancy is enabled for the actor type.
// When reentrancy is on, daprd auto-generates an id on each top-level call;
// the app must observe that same id on the incoming OnActorInvoke.
type reentrancy struct {
	daprd *daprd.Daprd
	place *placement.Placement

	mu      sync.Mutex
	seenIDs []string
}

func (r *reentrancy) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return r.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities: []string{"myactortype"},
				Reentrancy: &rtv1.ActorReentrancyConfig{
					Enabled: true,
				},
			}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
			// With streaming the reentrancy id travels inside the request
			// metadata map instead of gRPC metadata: the app is the gRPC
			// client, so daprd can't push per-message headers.
			r.mu.Lock()
			r.seenIDs = append(r.seenIDs, req.GetMetadata()["Dapr-Reentrancy-Id"])
			r.mu.Unlock()
			return &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{}, nil
		}),
	)

	r.place = placement.New(t)
	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, r.daprd, srv),
	}
}

func (r *reentrancy) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(r.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Two independent invocations. With reentrancy enabled daprd generates
	// a distinct id per top-level call, so the app should observe two
	// non-empty ids.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "first",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: "myactortype",
		ActorId:   "a",
		Method:    "second",
	})
	require.NoError(t, err)

	r.mu.Lock()
	defer r.mu.Unlock()
	require.GreaterOrEqual(t, len(r.seenIDs), 2, "expected at least two invocations")
	// Both ids must be non-empty (daprd generated them).
	for i, id := range r.seenIDs {
		assert.NotEmpty(t, id, "invocation %d had no Dapr-Reentrancy-Id metadata", i)
	}
	// Each top-level InvokeActor call is a fresh reentrancy root, so the
	// first and last observed ids must differ.
	assert.NotEqual(t, r.seenIDs[0], r.seenIDs[len(r.seenIDs)-1],
		"each top-level invocation must receive a fresh reentrancy id")
}
