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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/durationpb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(idlestream))
}

type idlestream struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler

	deactivated atomic.Int64
	fired       atomic.Int64
}

func (i *idlestream) Setup(t *testing.T) []framework.Option {
	srv := procgrpcapp.New(t,
		procgrpcapp.WithDaprdGRPCAddrFn(func() string { return i.daprd.GRPCAddress() }),
		procgrpcapp.WithActorRegistration(func() *rtv1.SubscribeActorEventsRequestInitialAlpha1 {
			return &rtv1.SubscribeActorEventsRequestInitialAlpha1{
				Entities:         []string{"abc"},
				ActorIdleTimeout: durationpb.New(time.Second),
			}
		}),
		procgrpcapp.WithOnActorTimerFn(func(context.Context, *rtv1.SubscribeActorEventsResponseTimerRequestAlpha1) (*rtv1.SubscribeActorEventsRequestReminderResponseAlpha1, error) {
			i.fired.Add(1)
			return &rtv1.SubscribeActorEventsRequestReminderResponseAlpha1{}, nil
		}),
		procgrpcapp.WithOnActorDeactivateFn(func(context.Context, *rtv1.SubscribeActorEventsResponseDeactivateRequestAlpha1) (*rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1, error) {
			i.deactivated.Add(1)
			return &rtv1.SubscribeActorEventsRequestDeactivateResponseAlpha1{}, nil
		}),
	)

	i.place = placement.New(t)
	i.sched = scheduler.New(t)
	i.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(i.place.Address()),
		daprd.WithScheduler(i.sched),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(i.sched, i.place, i.daprd, srv),
	}
}

func (i *idlestream) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta := i.daprd.GetMetadata(c, ctx).ActorRuntime
		assert.True(c, meta.HostReady)
		assert.ElementsMatch(c, []*daprd.MetadataActorRuntimeActiveActor{{Type: "abc"}}, meta.ActiveActors)
	}, time.Second*10, time.Millisecond*10)

	client := i.daprd.GRPCClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := client.RegisterActorTimer(ctx, &rtv1.RegisterActorTimerRequest{
			ActorType: "abc",
			ActorId:   "foo",
			Name:      "foo",
			DueTime:   "0s",
			Period:    "3s",
		})
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, i.fired.Load())
	}, time.Second*10, time.Millisecond*10)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Positive(c, i.deactivated.Load())
	}, time.Second*10, time.Millisecond*10)

	fired := i.fired.Load()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, i.fired.Load(), fired)
	}, time.Second*10, time.Millisecond*10)

	gauge := i.daprd.Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_timers", "actor_type:abc")
	assert.InDelta(t, float64(1), gauge, 0)
}
