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
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(reminderReentrancy))
}

type reminderReentrancy struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	reminderIDs slice.Slice[string]
	timerIDs    slice.Slice[string]
}

func (r *reminderReentrancy) Setup(t *testing.T) []framework.Option {
	r.reminderIDs = slice.New[string]()
	r.timerIDs = slice.New[string]()

	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return r.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities:   []string{"myactortype"},
				Reentrancy: &rtv1.ActorReentrancyConfig{Enabled: true},
			}
		}),
		procgrpcapp.WithOnActorReminderFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseReminderRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			r.reminderIDs.Append(req.GetMetadata()["Dapr-Reentrancy-Id"])
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorTimerFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			r.timerIDs.Append(req.GetMetadata()["Dapr-Reentrancy-Id"])
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
	)

	r.place = placement.New(t)
	r.sched = scheduler.New(t)
	r.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithScheduler(r.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.place, r.daprd, srv),
	}
}

func (r *reminderReentrancy) Run(t *testing.T, ctx context.Context) {
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
			ActorId:   "actor-1",
			Method:    "warmup",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "actor-1",
		Name:      "tick",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)
	_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "myactortype",
		ActorId:   "actor-1",
		Name:      "tock",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, r.reminderIDs.Slice())
		assert.NotEmpty(c, r.timerIDs.Slice())
	}, 10*time.Second, 10*time.Millisecond)

	for _, id := range append(r.reminderIDs.Slice(), r.timerIDs.Slice()...) {
		assert.NotEmpty(t, id)
	}
}
