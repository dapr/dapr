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

package restart

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcstream))
}

// grpcstream checks the activation id on the gRPC callback stream.
type grpcstream struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	rec         activation.Recorder
	deactivated atomic.Bool
}

func (g *grpcstream) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return g.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities:         []string{"abc"},
				ActorIdleTimeout: durationpb.New(time.Second * 2),
			}
		}),
		procgrpcapp.WithOnActorInvokeFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseInvokeRequestAlpha1) (*rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1, error) {
			g.rec.Record("invoke", activation.FromMetadata(req.GetMetadata()))
			return &rtv1.SubscribeActorEventsRequestInvokeResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorTimerFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			g.rec.Record("timer", activation.FromMetadata(req.GetMetadata()))
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorReminderFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseReminderRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			g.rec.Record("reminder", activation.FromMetadata(req.GetMetadata()))
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorDeactivateFn(func(context.Context, *rtv1.SubscribeActorEventsResponseDeactivateRequestAlpha1) (*rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1, error) {
			g.deactivated.Store(true)
			return &rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1{}, nil
		}),
	)

	g.place = placement.New(t)
	g.sched = scheduler.New(t)
	g.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(g.place.Address()),
		daprd.WithScheduler(g.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(g.sched, g.place, g.daprd, srv),
	}
}

func (g *grpcstream) Run(t *testing.T, ctx context.Context) {
	g.place.WaitUntilRunning(t, ctx)
	g.sched.WaitUntilRunning(t, ctx)
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)
	invoke := func() {
		activation.Invoke(t, ctx, client, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "foo"})
	}

	invoke()
	_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "abc", ActorId: "1", Name: "t", DueTime: "0s", Period: "1s",
	})
	require.NoError(t, err)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "abc", ActorId: "1", Name: "r", DueTime: "0s", Period: "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, activation.FirstIDs(g.rec.Peek()), 3)
	}, time.Second*8, time.Millisecond*10)

	_, err = client.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{ActorType: "abc", ActorId: "1", Name: "t"})
	require.NoError(t, err)
	_, err = client.UnregisterActorReminder(ctx, &rtv1.UnregisterActorReminderRequest{ActorType: "abc", ActorId: "1", Name: "r"})
	require.NoError(t, err)

	ids := activation.FirstIDs(g.rec.Take())
	require.Len(t, ids, 3)
	first := ids["invoke"]
	require.NotEmpty(t, first)
	assert.Equal(t, first, ids["timer"])
	assert.Equal(t, first, ids["reminder"])

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, g.deactivated.Load())
	}, time.Second*8, time.Millisecond*10)
	g.rec.Take()

	invoke()
	next := activation.FirstIDs(g.rec.Take())["invoke"]
	require.NotEmpty(t, next)
	assert.NotEqual(t, first, next)
}
