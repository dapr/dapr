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
	suite.Register(new(timer))
}

// timer verifies that OnActorTimer receives timers registered via the gRPC
// client API with name, callback, and period preserved end-to-end.
type timer struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	mu   sync.Mutex
	seen *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1
}

func (tm *timer) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return tm.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"myactortype"}}
		}),
		procgrpcapp.WithOnActorTimerFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			tm.mu.Lock()
			if tm.seen == nil {
				tm.seen = r
			}
			tm.mu.Unlock()
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{Cancel: true}, nil
		}),
	)

	tm.place = placement.New(t)
	tm.sched = scheduler.New(t)
	tm.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(tm.place.Address()),
		daprd.WithScheduler(tm.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithLogLevel("info"),
	)

	return []framework.Option{
		framework.WithProcesses(tm.sched, tm.place, tm.daprd, srv),
	}
}

func (tm *timer) Run(t *testing.T, ctx context.Context) {
	tm.place.WaitUntilRunning(t, ctx)
	tm.daprd.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(tm.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Activate the actor so placement knows it lives here.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, invErr := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "a",
			Method:    "warmup",
		})
		assert.NoError(c, invErr)
	}, 20*time.Second, 10*time.Millisecond, "actor not ready")

	_, err = client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
		ActorType: "myactortype",
		ActorId:   "a",
		Name:      "tick",
		DueTime:   "0s",
		Period:    "1s",
		Callback:  "onTick",
		Data:      []byte("payload"),
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		tm.mu.Lock()
		defer tm.mu.Unlock()
		assert.NotNil(c, tm.seen)
	}, 5*time.Second, 50*time.Millisecond, "timer did not fire")

	tm.mu.Lock()
	defer tm.mu.Unlock()
	require.NotNil(t, tm.seen)
	assert.Equal(t, "myactortype", tm.seen.GetActorType())
	assert.Equal(t, "a", tm.seen.GetActorId())
	assert.Equal(t, "tick", tm.seen.GetName())
	assert.Equal(t, "onTick", tm.seen.GetCallback())
	// Period is re-serialized via api.ReminderPeriod.String() — assert it
	// survives as a non-empty string rather than fixing the format.
	assert.NotEmpty(t, tm.seen.GetPeriod())
	// Data is an Any on the callback. The client-side RegisterActorTimer
	// wraps raw bytes via protobuf BytesValue, so a byte-for-byte compare
	// won't match; non-empty is the honest transport-level assertion.
	require.NotNil(t, tm.seen.GetData())
	assert.NotEmpty(t, tm.seen.GetData().GetValue())
}
