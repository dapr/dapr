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
	"google.golang.org/protobuf/types/known/durationpb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deactivation))
}

// deactivation verifies that after the configured idle timeout elapses,
// daprd issues OnActorDeactivate to the gRPC app for each idle actor.
// idle_timeout is advertised by the app via GetRegisteredActors.
type deactivation struct {
	daprd *daprd.Daprd
	place *placement.Placement

	deactivatedType atomic.Value // string
	deactivatedID   atomic.Value // string
	deactivations   atomic.Int32
}

func (d *deactivation) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return d.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities:         []string{"myactortype"},
				ActorIdleTimeout: durationpb.New(time.Second),
			}
		}),
		procgrpcapp.WithOnActorDeactivateFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseDeactivateRequestAlpha1) (*rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1, error) {
			d.deactivatedType.Store(r.GetActorType())
			d.deactivatedID.Store(r.GetActorId())
			d.deactivations.Add(1)
			return &rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1{}, nil
		}),
	)

	d.place = placement.New(t)
	d.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(d.place.Address()),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(d.place, d.daprd, srv),
	}
}

func (d *deactivation) Run(t *testing.T, ctx context.Context) {
	d.place.WaitUntilRunning(t, ctx)
	d.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(d.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Activate the actor once.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "idle-actor",
			Method:    "warmup",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	// Idle timeout is 1s; wait long enough for the idler to fire.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, d.deactivations.Load(), int32(1))
	}, 15*time.Second, 50*time.Millisecond, "OnActorDeactivate was not called after idle timeout")

	assert.Equal(t, "myactortype", d.deactivatedType.Load())
	assert.Equal(t, "idle-actor", d.deactivatedID.Load())
}
