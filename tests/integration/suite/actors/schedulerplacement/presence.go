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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(presence))
}

// presence asserts the probe of sidecar-reported placement addresses alone
// drives the authority: the leader is withheld while the reported placement
// service runs, and advertised once it is gone.
type presence struct {
	sched *scheduler.Scheduler
	place *placement.Placement
}

func (p *presence) Setup(t *testing.T) []framework.Option {
	p.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	p.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(p.sched, p.place),
	}
}

func (p *presence) Run(t *testing.T, ctx context.Context) {
	p.sched.WaitUntilRunning(t, ctx)
	p.place.WaitUntilRunning(t, ctx)

	// A capable sidecar reports its configured placement address, and the
	// probe finds the placement service running there.
	p.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
		PlacementAddresses:         []string{p.place.Address()},
	})

	leader := func() bool {
		stream, err := p.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
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

	// The reported placement service withholds the leader. The probe has
	// answered once the masked capability bit is broadcast, so the withhold
	// is settled state, not a boot race.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := p.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, err) {
			return
		}
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}
		for _, host := range resp.GetHosts() {
			assert.False(c, host.GetSchedulerPlacementEnabled())
			assert.False(c, host.GetLeader())
		}
	}, time.Second*20, time.Millisecond*50)

	p.place.Cleanup(t)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, leader())
	}, time.Second*30, time.Millisecond*50)
}
