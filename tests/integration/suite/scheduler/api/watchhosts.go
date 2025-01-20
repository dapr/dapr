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

package api

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
	suite.Register(new(watchhosts))
}

type watchhosts struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler
	scheduler4 *scheduler.Scheduler

	s1addr string
	s2addr string
	s3addr string
	s4addr string
}

func (w *watchhosts) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
		scheduler.WithInitialClusterPorts(port1, port2, port3),
	}

	clientPorts := []string{
		"scheduler-0=" + strconv.Itoa(fp.Port(t)),
		"scheduler-1=" + strconv.Itoa(fp.Port(t)),
		"scheduler-2=" + strconv.Itoa(fp.Port(t)),
	}

	w.scheduler1 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-0"), scheduler.WithEtcdClientPorts(clientPorts))...)
	w.scheduler2 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-1"), scheduler.WithEtcdClientPorts(clientPorts))...)
	w.scheduler3 = scheduler.New(t, append(opts, scheduler.WithID("scheduler-2"), scheduler.WithEtcdClientPorts(clientPorts))...)

	w.scheduler4 = scheduler.New(t,
		scheduler.WithID(w.scheduler2.ID()),
		scheduler.WithEtcdClientPorts(clientPorts),
		scheduler.WithInitialCluster(w.scheduler2.InitialCluster()),
		scheduler.WithDataDir(w.scheduler2.DataDir()),
		scheduler.WithPort(w.scheduler2.Port()),
	)

	w.s1addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(w.scheduler1.Port()))
	w.s2addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(w.scheduler2.Port()))
	w.s3addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(w.scheduler3.Port()))
	w.s4addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(w.scheduler4.Port()))

	return []framework.Option{
		framework.WithProcesses(fp),
	}
}

func (w *watchhosts) Run(t *testing.T, ctx context.Context) {
	w.scheduler1.Run(t, ctx)
	w.scheduler2.Run(t, ctx)
	w.scheduler3.Run(t, ctx)
	t.Cleanup(func() {
		w.scheduler1.Cleanup(t)
		w.scheduler2.Cleanup(t)
		w.scheduler3.Cleanup(t)
	})
	w.scheduler1.WaitUntilRunning(t, ctx)
	w.scheduler2.WaitUntilRunning(t, ctx)
	w.scheduler3.WaitUntilRunning(t, ctx)

	var stream schedulerv1pb.Scheduler_WatchHostsClient
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error
		stream, err = w.scheduler3.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, err) {
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

		assert.ElementsMatch(c, []string{
			w.s1addr,
			w.s2addr,
			w.s3addr,
		}, got)
	}, time.Second*20, time.Millisecond*10)

	w.scheduler2.Cleanup(t)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}
		got := make([]string, 0, 2)
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}
		assert.ElementsMatch(c, []string{
			w.s1addr,
			w.s3addr,
		}, got)
	}, time.Second*20, time.Millisecond*10)

	w.scheduler4.Run(t, ctx)
	w.scheduler4.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { w.scheduler4.Cleanup(t) })

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := stream.Recv()
		require.NoError(t, err)

		got := make([]string, 0, 3)
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}

		assert.ElementsMatch(c, []string{
			w.s1addr,
			w.s3addr,
			w.s4addr,
		}, got)
	}, time.Second*20, time.Millisecond*10)
}
