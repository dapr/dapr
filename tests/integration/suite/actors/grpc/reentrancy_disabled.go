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
	suite.Register(new(reentrancyDisabled))
}

// reentrancyDisabled is the opt-out counterpart to the reentrancy test:
// when the app does not enable reentrancy, daprd must not thread a
// Dapr-Reentrancy-Id onto the outgoing gRPC metadata. Locks the
// transport's "opt in" semantics so a future refactor can't silently start
// leaking the header to apps that haven't asked for it.
type reentrancyDisabled struct {
	daprd *daprd.Daprd
	place *placement.Placement

	mu      sync.Mutex
	seenIDs []string
}

func (r *reentrancyDisabled) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return r.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			// Note: no Reentrancy config — default is disabled.
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
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

func (r *reentrancyDisabled) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(r.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "ping",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.seenIDs, "app should have been invoked at least once")
	for i, id := range r.seenIDs {
		assert.Empty(t, id,
			"invocation %d leaked a Dapr-Reentrancy-Id header with reentrancy disabled", i)
	}
}
