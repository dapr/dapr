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

package remote

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(stream))
}

type stream struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	timer1, timer2 atomic.Int64
}

func (s *stream) Setup(t *testing.T) []framework.Option {
	registration := func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
		return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"abc"}}
	}

	srv1 := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return s.daprd1.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(registration),
		procgrpcapp.WithOnActorTimerFn(func(context.Context, *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			s.timer1.Add(1)
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
	)
	srv2 := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return s.daprd2.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(registration),
		procgrpcapp.WithOnActorTimerFn(func(context.Context, *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			s.timer2.Add(1)
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
	)

	s.place = placement.New(t)
	s.sched = scheduler.New(t)
	s.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv1.Port(t)),
	)
	s.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv2.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.place, s.daprd1, s.daprd2, srv1, srv2),
	}
}

func (s *stream) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, s.daprd1.GetMetadata(c, ctx).ActorRuntime.HostReady)
		assert.True(c, s.daprd2.GetMetadata(c, ctx).ActorRuntime.HostReady)
	}, time.Second*10, time.Millisecond*10)

	client1 := s.daprd1.GRPCClient(t, ctx)
	client2 := s.daprd2.GRPCClient(t, ctx)

	register := func(client rtv1.DaprClient, id string) error {
		_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   id,
			Name:      "foo",
			DueTime:   "0s",
			Period:    "1s",
		})
		return err
	}

	// Both hosts have registered with placement, but wait until the table has
	// settled so ownership answers are stable.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		err1 := register(client1, "probe")
		err2 := register(client2, "probe")
		assert.NotEqual(c, err1 == nil, err2 == nil)
	}, time.Second*10, time.Millisecond*10)

	owner1, owner2 := "", ""
	for i := 0; owner1 == "" || owner2 == ""; i++ {
		require.Less(t, i, 100, "actor IDs never hashed to both hosts")
		id := strconv.Itoa(i)
		err1 := register(client1, id)
		err2 := register(client2, id)
		require.False(t, err1 == nil && err2 == nil, "both hosts accepted the same timer registration")
		require.False(t, err1 != nil && err2 != nil, "both hosts rejected the timer registration")
		if err1 == nil {
			require.Equal(t, grpccodes.PermissionDenied, status.Code(err2))
			if owner1 == "" {
				owner1 = id
			}
		} else {
			require.Equal(t, grpccodes.PermissionDenied, status.Code(err1))
			if owner2 == "" {
				owner2 = id
			}
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, s.timer1.Load())
		assert.Positive(c, s.timer2.Load())
	}, time.Second*10, time.Millisecond*10)

	_, err := client2.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner1, Name: "foo",
	})
	require.Equal(t, grpccodes.PermissionDenied, status.Code(err))
	_, err = client1.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner1, Name: "foo",
	})
	require.NoError(t, err)
	_, err = client2.UnregisterActorTimer(ctx, &rtv1.UnregisterActorTimerRequest{
		ActorType: "abc", ActorId: owner2, Name: "foo",
	})
	require.NoError(t, err)
}
