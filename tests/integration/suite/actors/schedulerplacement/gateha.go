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

// gateha asserts the capability gate is replicated: an old sidecar connected
// to one scheduler withholds the placement advertisement on every scheduler
// in the cluster, so no scheduler ever advertises a different authority than
// its peers.
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

	// An old sidecar on scheduler 1 engages the gate, observed replicated
	// on every scheduler before the capable sidecar connects.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	g.schedulers[1].WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})

	masked := func(sched *scheduler.Scheduler) bool {
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
			if host.GetSchedulerPlacementEnabled() {
				return false
			}
		}
		return len(resp.GetHosts()) > 0
	}
	for _, sched := range g.schedulers {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, masked(sched), "the gate must replicate to every scheduler")
		}, time.Second*20, time.Millisecond*50)
	}

	// A capable sidecar on scheduler 0 makes the cluster eligible to
	// advertise, and the replicated gate must withhold it everywhere.
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

	assert.Never(t, func() bool {
		return leaderOn(g.schedulers[2])
	}, time.Second*5, time.Millisecond*500,
		"an old sidecar on one scheduler must withhold the advertisement on all of them")

	// The old sidecar disconnects: every scheduler advertises the same
	// single leader.
	oldCancel()
	for _, sched := range g.schedulers {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, leaderOn(sched))
		}, time.Second*20, time.Millisecond*50)
	}
}
