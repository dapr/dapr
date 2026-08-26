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

package schedulerplacement

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(gateha))
}

// gateha asserts an old sidecar does not withhold the placement
// advertisement in an HA cluster: with no placement service visible nothing
// can serve it. The old sidecar is counted, and each scheduler advertises
// once a capable sidecar connects to it.
type gateha struct {
	schedulers [3]*scheduler.Scheduler
}

func (g *gateha) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithPlacementEnabled(true),
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	g.schedulers[0] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPort(port4))...)
	g.schedulers[1] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPort(port5))...)
	g.schedulers[2] = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPort(port6))...)

	return []framework.Option{
		framework.WithProcesses(fp, g.schedulers[0], g.schedulers[1], g.schedulers[2]),
	}
}

func (g *gateha) Run(t *testing.T, ctx context.Context) {
	for _, sched := range g.schedulers {
		sched.WaitUntilRunning(t, ctx)
	}

	// An old sidecar on scheduler 1 is counted, but the schedulers keep
	// advertising their placement capability: with no placement service
	// visible, nothing could serve it.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	g.schedulers[1].WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		all := g.schedulers[1].Metrics(c, ctx).All()
		assert.Equal(c, 1, int(all["dapr_scheduler_placement_incapable_sidecars"]))
	}, time.Second*20, time.Millisecond*50)

	schedulerPlacementEnabled := func(sched *scheduler.Scheduler) bool {
		stream, err := sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if !host.GetSchedulerPlacementEnabled() {
				return false
			}
		}
		return len(resp.GetHosts()) > 0
	}
	for _, sched := range g.schedulers {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, schedulerPlacementEnabled(sched), "an old sidecar must not hide the scheduler placement capability")
		}, time.Second*20, time.Millisecond*50)
	}

	// A capable sidecar on scheduler 0 makes it advertise the leader, old
	// sidecar or not.
	g.schedulers[0].WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "new-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})

	leaderOn := func(sched *scheduler.Scheduler) bool {
		stream, err := sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if err != nil {
			return false
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if err != nil {
			return false
		}
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, leaderOn(g.schedulers[0]),
			"an old sidecar elsewhere must not withhold the advertisement")
	}, time.Second*20, time.Millisecond*50)

	// The other schedulers have no capable sidecar of their own yet, so
	// they stay leaderless. Once one connects to each, every scheduler
	// advertises.
	oldCancel()
	for _, sched := range []*scheduler.Scheduler{g.schedulers[1], g.schedulers[2]} {
		sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
			AppId:                      "new-sidecar-2",
			Namespace:                  "default",
			SupportsSchedulerPlacement: true,
		})
	}
	for _, sched := range g.schedulers {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, leaderOn(sched))
		}, time.Second*20, time.Millisecond*50)
	}
}
