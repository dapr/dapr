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

package coldstart

import (
	"context"
	"strconv"
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
	suite.Register(new(broadcast))
}

// broadcast asserts a fresh scheduler never says it does not serve
// placement while it checks a sidecar-reported placement address. A sidecar
// that hears that gives up on the scheduler and switches to the placement
// service, even when nothing is running there. During the check the
// scheduler may hold back the leader, which makes sidecars wait.
type broadcast struct {
	sched         *scheduler.Scheduler
	deadPlacement string
}

func (c *broadcast) Setup(t *testing.T) []framework.Option {
	c.deadPlacement = "127.0.0.1:" + strconv.Itoa(ports.Reserve(t, 1).Port(t))
	c.sched = scheduler.New(t, scheduler.WithPlacementEnabled(true))

	return []framework.Option{
		framework.WithProcesses(c.sched),
	}
}

func (c *broadcast) Run(t *testing.T, ctx context.Context) {
	c.sched.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	stream, err := c.sched.Client(t, sctx).WatchHosts(sctx, new(schedulerv1pb.WatchHostsRequest))
	require.NoError(t, err)

	broadcasts := make(chan []*schedulerv1pb.Host, 64)
	go func() {
		defer close(broadcasts)
		for {
			resp, rerr := stream.Recv()
			if rerr != nil {
				return
			}
			broadcasts <- resp.GetHosts()
		}
	}()

	next := func() []*schedulerv1pb.Host {
		t.Helper()
		select {
		case hosts, ok := <-broadcasts:
			require.True(t, ok, "WatchHosts stream closed")
			return hosts
		case <-time.After(time.Second * 20):
			require.Fail(t, "timed out waiting for a WatchHosts broadcast")
			return nil
		}
	}

	leader := func(hosts []*schedulerv1pb.Host) string {
		for _, host := range hosts {
			if host.GetLeader() {
				return host.GetAddress()
			}
		}
		return ""
	}

	first := next()
	require.NotEmpty(t, first)
	for _, host := range first {
		assert.True(t, host.GetSchedulerPlacementEnabled(), "initial broadcast must carry the capability")
		assert.False(t, host.GetLeader())
	}

	c.sched.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "capable-sidecar",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
		PlacementAddresses:         []string{c.deadPlacement},
	})

	var seen int
	for {
		hosts := next()
		seen++
		require.NotEmpty(t, hosts)
		for _, host := range hosts {
			require.True(t, host.GetSchedulerPlacementEnabled(),
				"broadcast %d reported not serving placement while no placement service was observed: %v", seen, hosts)
		}
		if addr := leader(hosts); addr != "" {
			assert.Equal(t, c.sched.Address(), addr)
			break
		}
	}
}
