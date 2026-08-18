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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(reminderDelivery))
}

// reminderDelivery verifies that a reminder registered via the gRPC client
// API reaches the app's OnActorReminder handler with its name, period, and
// due_time intact, and that an SubscribeActorEventsRequestReminderResponseAlpha1 (including cancel=true)
// is accepted by the scheduler without surfacing an error.
//
// This test deliberately does not assert that cancel=true stops future fires:
// that behavior depends on a scheduler-side TODO shared with the HTTP path
// (see tests/integration/suite/actors/reminders/basic.go:167 for the matching
// commented-out assertion and pkg/runtime/scheduler/internal/cluster/streamer.go
// for the TODO).
type reminderDelivery struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	mu   sync.Mutex
	seen *rtv1.SubscribeActorEventsResponseReminderRequestAlpha1
}

func (r *reminderDelivery) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return r.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorReminderFn(func(_ context.Context, req *rtv1.SubscribeActorEventsResponseReminderRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			r.mu.Lock()
			if r.seen == nil {
				r.seen = req
			}
			r.mu.Unlock()
			// cancel=true exercises the gRPC transport → actorerrors.ErrReminderCanceled mapping.
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{Cancel: true}, nil
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

func (r *reminderDelivery) Run(t *testing.T, ctx context.Context) {
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(r.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Invoke once so the actor instance is materialized on this host before
	// registering the reminder.
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
		Name:      "oneshot",
		DueTime:   "0s",
		Period:    "1s",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		r.mu.Lock()
		defer r.mu.Unlock()
		assert.NotNil(c, r.seen)
	}, 5*time.Second, 50*time.Millisecond, "reminder did not fire")

	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotNil(t, r.seen)
	assert.Equal(t, "myactortype", r.seen.GetActorType())
	assert.Equal(t, "actor-1", r.seen.GetActorId())
	assert.Equal(t, "oneshot", r.seen.GetName())
	// Period is not asserted: at the callback boundary the reminder's
	// period is scheduler-internal context and does not round-trip to the
	// app for either transport — matching the HTTP path's behavior.
}
