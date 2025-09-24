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

package watchhosts

import (
	"context"
	"fmt"
	"net"
	"runtime"
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
	suite.Register(new(cluster))
}

type cluster struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler
	scheduler4 *scheduler.Scheduler

	s1addr string
	s2addr string
	s3addr string
	s4addr string
}

func (c *cluster) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	c.scheduler1 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPort(port4))...)
	c.scheduler2 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPort(port5))...)
	c.scheduler3 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPort(port6))...)

	c.scheduler4 = scheduler.New(t,
		scheduler.WithID(c.scheduler2.ID()),
		scheduler.WithEtcdClientPort(port5),
		scheduler.WithInitialCluster(c.scheduler2.InitialCluster()),
		scheduler.WithDataDir(c.scheduler2.DataDir()),
		scheduler.WithPort(c.scheduler2.Port()),
	)

	c.s1addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(c.scheduler1.Port()))
	c.s2addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(c.scheduler2.Port()))
	c.s3addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(c.scheduler3.Port()))
	c.s4addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(c.scheduler4.Port()))

	return []framework.Option{
		framework.WithProcesses(fp),
	}
}

func (c *cluster) Run(t *testing.T, ctx context.Context) {
	c.scheduler1.Run(t, ctx)
	c.scheduler2.Run(t, ctx)
	c.scheduler3.Run(t, ctx)
	t.Cleanup(func() {
		c.scheduler1.Cleanup(t)
		c.scheduler2.Cleanup(t)
		c.scheduler3.Cleanup(t)
	})
	c.scheduler1.WaitUntilRunning(t, ctx)
	c.scheduler2.WaitUntilRunning(t, ctx)
	c.scheduler3.WaitUntilRunning(t, ctx)

	var stream schedulerv1pb.Scheduler_WatchHostsClient
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		var err error
		stream, err = c.scheduler3.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(col, err) {
			return
		}

		t.Cleanup(func() {
			require.NoError(t, stream.CloseSend())
		})

		resp, err := stream.Recv()
		require.NoError(t, err)

		got := make([]string, 0, 3)
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}

		assert.ElementsMatch(col, []string{
			c.s1addr,
			c.s2addr,
			c.s3addr,
		}, got)
	}, time.Second*20, time.Millisecond*10)

	c.scheduler2.Kill(t)

	require.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, err := stream.Recv()
		if !assert.NoError(col, err) {
			stream, err = c.scheduler3.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
			require.NoError(t, err)
			return
		}
		got := make([]string, 0, 2)
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}
		assert.ElementsMatch(col, []string{
			c.s1addr,
			c.s3addr,
		}, got)
	}, time.Second*30, time.Millisecond*10)

	c.scheduler4.Run(t, ctx)
	c.scheduler4.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { c.scheduler4.Kill(t) })

	require.EventuallyWithT(t, func(col *assert.CollectT) {
		resp, err := stream.Recv()
		require.NoError(t, err)

		got := make([]string, 0, 3)
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}

		assert.ElementsMatch(col, []string{
			c.s1addr,
			c.s3addr,
			c.s4addr,
		}, got)
	}, time.Second*20, time.Millisecond*10)
}
