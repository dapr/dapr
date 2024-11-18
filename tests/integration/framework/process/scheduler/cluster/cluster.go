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

	fp := ports.Reserve(t, int(opts.count)*5)

	uids := make([]string, opts.count)
	ports := make([]int, opts.count)
	healthzPorts := make([]int, opts.count)
	metricsPorts := make([]int, opts.count)
	initialCluster := make([]string, opts.count)
	clientPorts := make([]string, opts.count)

	for i := range ports {
		uids[i] = "scheduler-" + strconv.Itoa(i)
		ports[i] = fp.Port(t)
		healthzPorts[i] = fp.Port(t)
		metricsPorts[i] = fp.Port(t)
		initialCluster[i] = fmt.Sprintf("%s=http://127.0.0.1:%d", uids[i], fp.Port(t))
		clientPorts[i] = fmt.Sprintf("%s=%d", uids[i], fp.Port(t))
	}

	schedulers := make([]*scheduler.Scheduler, opts.count)
	for i := range opts.count {
		schedulers[i] = scheduler.New(t,
			scheduler.WithID(uids[i]),
			scheduler.WithPort(ports[i]),
			scheduler.WithHealthzPort(healthzPorts[i]),
			scheduler.WithMetricsPort(metricsPorts[i]),
			scheduler.WithInitialCluster(strings.Join(initialCluster, ",")),
			scheduler.WithEtcdClientPorts(clientPorts),
			scheduler.WithReplicaCount(opts.count),
		)
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
}

func (c *Cluster) Client(t *testing.T, ctx context.Context) schedulerv1pb.SchedulerClient {
	t.Helper()
	return c.schedulers[0].Client(t, ctx)
}

func (c *Cluster) Addresses() []string {
	addrs := make([]string, len(c.schedulers))
	for i, s := range c.schedulers {
		addrs[i] = s.Address()
	}
	return addrs
}
