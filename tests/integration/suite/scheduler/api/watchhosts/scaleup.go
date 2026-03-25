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
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(scaleup))
}

type scaleup struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler

	s1addr string
	s2addr string
	s3addr string
}

func (s *scaleup) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup does not work cleanly on windows")
	}

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	port4, port5, port6 := fp.Port(t), fp.Port(t), fp.Port(t)

	clusterOpts := []scheduler.Option{
		scheduler.WithInitialCluster(fmt.Sprintf(
			"scheduler-0=http://127.0.0.1:%d,scheduler-1=http://127.0.0.1:%d,scheduler-2=http://127.0.0.1:%d",
			port1, port2, port3),
		),
	}

	s.scheduler1 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-0"),
		scheduler.WithEtcdClientPort(port4),
	)...)
	s.scheduler2 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-1"),
		scheduler.WithEtcdClientPort(port5),
	)...)
	s.scheduler3 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-2"),
		scheduler.WithEtcdClientPort(port6),
	)...)

	s.s1addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(s.scheduler1.Port()))
	s.s2addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(s.scheduler2.Port()))
	s.s3addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(s.scheduler3.Port()))

	return []framework.Option{
		framework.WithProcesses(fp, s.scheduler1, s.scheduler2),
	}
}

func (s *scaleup) Run(t *testing.T, ctx context.Context) {
	s.scheduler1.WaitUntilRunning(t, ctx)
	s.scheduler2.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := s.scheduler1.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, err) {
			return
		}
		defer stream.CloseSend()
		resp, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, resp.GetHosts(), 2)
	}, time.Second*20, time.Millisecond*10)

	const numWatchers = 500
	for i := range numWatchers {
		sched := s.scheduler1
		if i%2 == 1 {
			sched = s.scheduler2
		}
		stream, err := sched.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		require.NoError(t, err, "failed to create WatchHosts stream %d", i)

		_, err = stream.Recv()
		require.NoError(t, err, "failed to recv initial WatchHosts %d", i)

		t.Cleanup(func() {
			require.NoError(t, stream.CloseSend())
		})
	}

	const numJobs = 400
	for i := range numJobs {
		sched := s.scheduler1
		if i%2 == 1 {
			sched = s.scheduler2
		}
		_, err := sched.Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "load-" + strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId: "load-app", Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{
						Job: new(schedulerv1pb.TargetJob),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	triggered := s.scheduler1.WatchJobsSuccess(t, ctx,
		&schedulerv1pb.WatchJobsRequestInitial{
			AppId: "post-scaleup", Namespace: "default",
		},
	)

	s.scheduler3.Run(t, ctx)
	t.Cleanup(func() { s.scheduler3.Cleanup(t) })
	s.scheduler3.WaitUntilRunning(t, ctx)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := s.scheduler1.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, err) {
			return
		}
		defer stream.CloseSend()

		resp, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}

		got := make([]string, 0, len(resp.GetHosts()))
		for _, host := range resp.GetHosts() {
			got = append(got, host.GetAddress())
		}

		assert.ElementsMatch(c, []string{s.s1addr, s.s2addr, s.s3addr}, got)
	}, time.Second*30, time.Millisecond*100)

	for i := range 30 {
		_, err := s.scheduler1.Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "post-" + strconv.Itoa(i),
			Job:  &schedulerv1pb.Job{DueTime: ptr.Of(time.Now().Format(time.RFC3339))},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId: "post-scaleup", Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{
						Job: new(schedulerv1pb.TargetJob),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case name := <-triggered:
			assert.Contains(c, name, "post-")
		default:
			assert.Fail(c, "no post-scaleup job triggered")
		}
	}, time.Second*20, time.Millisecond*10)
}
