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

package kubernetes

import (
	"context"
	"fmt"
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
	suite.Register(new(leadershipchurn))
}

type leadershipchurn struct {
	scheduler1 *scheduler.Scheduler
	scheduler2 *scheduler.Scheduler
	scheduler3 *scheduler.Scheduler
}

func (n *leadershipchurn) Setup(t *testing.T) []framework.Option {
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

	n.scheduler1 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-0"),
		scheduler.WithEtcdClientPort(port4),
	)...)
	n.scheduler2 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-1"),
		scheduler.WithEtcdClientPort(port5),
	)...)
	n.scheduler3 = scheduler.New(t, append(clusterOpts,
		scheduler.WithID("scheduler-2"),
		scheduler.WithEtcdClientPort(port6),
	)...)

	return []framework.Option{
		framework.WithProcesses(fp),
	}
}

func (n *leadershipchurn) Run(t *testing.T, ctx context.Context) {
	n.scheduler1.Run(t, ctx)
	n.scheduler2.Run(t, ctx)
	n.scheduler3.Run(t, ctx)
	t.Cleanup(func() {
		n.scheduler1.Cleanup(t)
		n.scheduler2.Cleanup(t)
		n.scheduler3.Cleanup(t)
	})
	n.scheduler1.WaitUntilRunning(t, ctx)
	n.scheduler2.WaitUntilRunning(t, ctx)
	n.scheduler3.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err := n.scheduler1.Client(t, ctx).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		if !assert.NoError(c, err) {
			return
		require.NoError(c, err)
		defer func() {
			require.NoError(t, stream.CloseSend())
		}()
		resp, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, resp.GetHosts(), 3)
	}, 20*time.Second, 10*time.Millisecond)

	triggered := n.scheduler1.WatchJobsSuccess(t, ctx,
		&schedulerv1pb.WatchJobsRequestInitial{
			AppId:     "churnapp",
			Namespace: "default",
		},
	)

	client := n.scheduler1.Client(t, ctx)

	// Schedule many immediate-fire oneshot jobs. Each triggers, completes, and
	// its counter emits a CloseJob to the router. Under high throughput with
	// leadership changes, stale CloseJob events can arrive for counters that
	// have already been cleaned up.
	const numJobs = 50
	for i := range numJobs {
		_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: "churn-" + strconv.Itoa(i),
			Job: &schedulerv1pb.Job{
				DueTime: ptr.Of(time.Now().Format(time.RFC3339)),
			},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "churnapp",
				Namespace: "default",
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
		count := 0
	drain:
		for {
			select {
			case <-triggered:
				count++
			default:
				break drain
			}
		}
		assert.Greater(c, count, 0)
	}, 10*time.Second, 10*time.Millisecond)

	n.scheduler2.Kill(t)

	for i := range 10 {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
				Name: "post-kill-" + strconv.Itoa(i),
				Job: &schedulerv1pb.Job{
					DueTime: ptr.Of(time.Now().Format(time.RFC3339)),
				},
				Metadata: &schedulerv1pb.JobMetadata{
					AppId:     "churnapp",
					Namespace: "default",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: new(schedulerv1pb.TargetJob),
						},
					},
				},
			})
			assert.NoError(c, err)
		}, 15*time.Second, 10*time.Millisecond)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		select {
		case name := <-triggered:
			assert.Contains(c, name, "post-kill")
		default:
			assert.Fail(c, "no post-kill job triggered yet")
		}
	}, 20*time.Second, 10*time.Millisecond)
}
