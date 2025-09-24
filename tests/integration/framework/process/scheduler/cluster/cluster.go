/*
Copyright 2024 The Dapr Authors
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

package cluster

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
)

type Cluster struct {
	fp         *ports.Ports
	schedulers []*scheduler.Scheduler
}

func New(t *testing.T, fopts ...Option) *Cluster {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.Positive(t, opts.count, "count must be positive")
	if len(opts.overrideBroadcastHostPorts) > 0 {
		require.Len(t, opts.overrideBroadcastHostPorts, int(opts.count), "overrideBroadcastHostPorts must have the same length as count")
	}

	fp := ports.Reserve(t, int(opts.count)*5)

	uids := make([]string, opts.count)
	ports := make([]int, opts.count)
	healthzPorts := make([]int, opts.count)
	metricsPorts := make([]int, opts.count)
	initialCluster := make([]string, opts.count)
	clientPorts := make([]int, opts.count)

	for i := range ports {
		uids[i] = "dapr-scheduler-server-" + strconv.Itoa(i)
		ports[i] = fp.Port(t)
		healthzPorts[i] = fp.Port(t)
		metricsPorts[i] = fp.Port(t)
		initialCluster[i] = fmt.Sprintf("%s=http://127.0.0.1:%d", uids[i], fp.Port(t))
		clientPorts[i] = fp.Port(t)
	}

	schedulers := make([]*scheduler.Scheduler, opts.count)
	for i := range opts.count {
		sopts := []scheduler.Option{
			scheduler.WithID(uids[i]),
			scheduler.WithPort(ports[i]),
			scheduler.WithHealthzPort(healthzPorts[i]),
			scheduler.WithMetricsPort(metricsPorts[i]),
			scheduler.WithInitialCluster(strings.Join(initialCluster, ",")),
			scheduler.WithEtcdClientPort(clientPorts[i]),
			scheduler.WithID("dapr-scheduler-server-" + strconv.FormatUint(uint64(i), 10)),
		}

		if len(opts.overrideBroadcastHostPorts) > 0 {
			sopts = append(sopts,
				scheduler.WithOverrideBroadcastHostPort(opts.overrideBroadcastHostPorts[i]),
			)
		}

		schedulers[i] = scheduler.New(t, sopts...)
	}

	return &Cluster{
		fp:         fp,
		schedulers: schedulers,
	}
}

func (c *Cluster) Run(t *testing.T, ctx context.Context) {
	t.Helper()

	c.fp.Free(t)
	for _, s := range c.schedulers {
		s.Run(t, ctx)
	}
}

func (c *Cluster) Cleanup(t *testing.T) {
	t.Helper()

	var wg sync.WaitGroup
	wg.Add(len(c.schedulers))
	for _, s := range c.schedulers {
		go func(s *scheduler.Scheduler) {
			s.Cleanup(t)
			wg.Done()
		}(s)
	}
	wg.Wait()
}

func (c *Cluster) WaitUntilRunning(t *testing.T, ctx context.Context) {
	for _, s := range c.schedulers {
		s.WaitUntilRunning(t, ctx)
	}

	for _, sched := range c.schedulers {
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			stream, err := sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
			require.NoError(t, err)
			resp, err := stream.Recv()
			stream.CloseSend()
			require.NoError(t, err)
			assert.Len(col, resp.GetHosts(), len(c.schedulers))
		}, 10*time.Second, 10*time.Millisecond)
	}
}

func (c *Cluster) Client(t *testing.T, ctx context.Context) schedulerv1pb.SchedulerClient {
	t.Helper()
	return c.ClientN(t, ctx, 0)
}

func (c *Cluster) ClientN(t *testing.T, ctx context.Context, n int) schedulerv1pb.SchedulerClient {
	t.Helper()
	require.Less(t, n, len(c.schedulers), "n must be less than the number of schedulers in the cluster")
	return c.schedulers[n].Client(t, ctx)
}

func (c *Cluster) EtcdClientPortN(t *testing.T, n int) int {
	t.Helper()
	require.Less(t, n, len(c.schedulers), "n must be less than the number of schedulers in the cluster")
	return c.schedulers[n].EtcdClientPort()
}

func (c *Cluster) Addresses() []string {
	addrs := make([]string, len(c.schedulers))
	for i, s := range c.schedulers {
		addrs[i] = s.Address()
	}
	return addrs
}
