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
	suite.Register(new(leader))
}

// leader asserts the scheduler only advertises a placement leader on
// WatchHosts once a sidecar supporting scheduler placement is connected.
// scheduler_placement_enabled is set from boot.
type leader struct {
	sched *scheduler.Scheduler
}

func (l *leader) Setup(t *testing.T) []framework.Option {
	l.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))
	return []framework.Option{
		framework.WithProcesses(l.sched),
	}
}

func (l *leader) Run(t *testing.T, ctx context.Context) {
	l.sched.WaitUntilRunning(t, ctx)

	stream, err := l.sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)
	//nolint:errcheck
	defer stream.CloseSend()
	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, resp.GetHosts(), 1)
	assert.True(t, resp.GetHosts()[0].GetSchedulerPlacementEnabled())
	assert.False(t, resp.GetHosts()[0].GetLeader())

	// A capable sidecar connects: the single scheduler becomes the leader.
	l.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	})

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, rerr := stream.Recv()
		if !assert.NoError(c, rerr) {
			return
		}
		if !assert.Len(c, resp.GetHosts(), 1) {
			return
		}
		assert.True(c, resp.GetHosts()[0].GetLeader())
	}, time.Second*20, time.Millisecond*50)
}
