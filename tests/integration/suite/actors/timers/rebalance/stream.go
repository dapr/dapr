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

package rebalance

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(stream))
}

type stream struct {
	daprd1 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	deactivated slice.Slice[string]
	fired1      slice.Slice[string]
}

func (s *stream) Setup(t *testing.T) []framework.Option {
	s.deactivated = slice.String()
	s.fired1 = slice.String()

	srv1 := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return s.daprd1.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"abc"}}
		}),
		procgrpcapp.WithOnActorTimerFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			s.fired1.Append(r.GetActorId())
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorDeactivateFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseDeactivateRequestAlpha1) (*rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1, error) {
			s.deactivated.Append(r.GetActorId())
			return &rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1{}, nil
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

	return []framework.Option{
		framework.WithProcesses(s.sched, s.place, s.daprd1, srv1),
	}
}

func (s *stream) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.daprd1.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta := s.daprd1.GetMetadata(c, ctx).ActorRuntime
		assert.True(c, meta.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{{Type: "abc"}}, meta.ActiveActors)
	}, time.Second*10, time.Millisecond*10)

	client1 := s.daprd1.GRPCClient(t, ctx)
	register := func(id string) error {
		_, err := client1.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   id,
			Name:      "foo",
			DueTime:   "0s",
			Period:    "1s",
		})
		return err
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, register("0"))
	}, time.Second*10, time.Millisecond*10)
	for i := 1; i < 100; i++ {
		require.NoError(t, register(strconv.Itoa(i)))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, s.fired1.Len())
	}, time.Second*10, time.Millisecond*10)

	fired2 := slice.String()
	var daprd2 *daprd.Daprd
	srv2 := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return daprd2.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{Entities: []string{"abc"}}
		}),
		procgrpcapp.WithOnActorTimerFn(func(_ context.Context, r *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			fired2.Append(r.GetActorId())
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
	)
	daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithScheduler(s.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv2.Port(t)),
	)
	t.Cleanup(func() { srv2.Cleanup(t) })
	t.Cleanup(func() { daprd2.Cleanup(t) })
	daprd2.Run(t, ctx)
	srv2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		deactivated := s.deactivated.Len()
		assert.Positive(c, deactivated)
		gauge := s.daprd1.Metrics(c, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
		assert.InDelta(c, float64(100-deactivated), gauge, 0)
	}, time.Second*20, time.Millisecond*10)

	moved := make(map[string]struct{})
	for _, id := range s.deactivated.Slice() {
		moved[id] = struct{}{}
	}
	before := s.fired1.Len()
	time.Sleep(time.Second * 3)

	after := s.fired1.Slice()
	assert.Greater(t, len(after), before, "timers of non-moved actors must keep firing")
	for _, id := range after[before:] {
		assert.NotContains(t, moved, id, "a moved actor's timer fired on its old host")
	}
	assert.Empty(t, fired2.Slice(), "a timer fire crossed to the actor's new host")
}
