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

package placement

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(gate))
}

// gate asserts the mixed version gate: a sidecar without
// SupportsSchedulerPlacement never makes the scheduler advertise a placement
// leader, a capable sidecar does, and once a sidecar opened a placement
// stream to the scheduler the advertisement survives every capable sidecar
// disconnecting.
type gate struct {
	sched *scheduler.Scheduler
}

func (g *gate) Setup(t *testing.T) []framework.Option {
	g.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(g.sched),
	}
}

func (g *gate) Run(t *testing.T, ctx context.Context) {
	g.sched.WaitUntilRunning(t, ctx)

	leader := func() bool {
		stream, err := g.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		require.NoError(t, err)
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		require.NoError(t, err)
		for _, host := range resp.GetHosts() {
			if host.GetLeader() {
				return true
			}
		}
		return false
	}

	// An old sidecar alone does not make the scheduler advertise.
	oldCtx, oldCancel := context.WithCancel(ctx)
	t.Cleanup(oldCancel)
	g.sched.WatchJobsSuccess(t, oldCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})
	time.Sleep(time.Second * 2)
	assert.False(t, leader(), "an old sidecar must not make the scheduler advertise a placement leader")

	// A capable sidecar does.
	capableCtx, capableCancel := context.WithCancel(ctx)
	g.sched.WatchJobsSuccess(t, capableCtx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, leader())
	}, time.Second*20, time.Millisecond*50)

	// Once a sidecar opens a placement stream to the scheduler, the
	// scheduler never again withholds the leader for lack of a capable
	// sidecar.
	var stream schedulerv1pb.Scheduler_ReportActorTypesClient
	streamCtx, streamCancel := context.WithCancel(ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		stream, err = g.sched.Client(t, streamCtx).ReportActorTypes(streamCtx)
		if !assert.NoError(c, err) {
			return
		}
		err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
				Address:    "127.0.0.1:40001",
				AppId:      "capable-sidecar",
				Namespace:  "default",
				ActorTypes: []string{"mytype"},
			}},
		})
		if !assert.NoError(c, err) {
			return
		}
		_, err = stream.Recv()
		assert.NoError(c, err)
	}, time.Second*20, time.Millisecond*100)

	// The capable sidecar disconnects while the placement stream to the
	// scheduler lives: the advertisement stays.
	capableCancel()
	time.Sleep(time.Second * 2)
	assert.True(t, leader(), "a live placement stream must keep the advertisement")

	// The placement stream closes too, and the old sidecar reconnects to
	// force recomputes: the advertisement survives with no capable sidecar
	// left, so an old sidecar joining a settled cluster cannot drop every
	// placement stream.
	streamCancel()
	oldCancel()
	time.Sleep(time.Second)
	g.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:     "old-sidecar",
		Namespace: "default",
	})
	time.Sleep(time.Second * 2)
	assert.True(t, leader(), "the advertisement must survive losing every capable sidecar")
}
