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

package quorum

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restartall))
}

// restartall verifies that a full cluster restart converges: every member
// starts its server exactly once instead of entering a teardown/rebuild loop
// (each member restarting its embedded etcd disturbs quorum for the others,
// whose just-elected leadership leases are the most fragile), and both stored
// jobs and the API survive the restart.
type restartall struct {
	cluster *cluster.Cluster
}

func (r *restartall) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.cluster = cluster.New(t, cluster.WithCount(3))

	return []framework.Option{
		framework.WithProcesses(r.cluster),
	}
}

func (r *restartall) Run(t *testing.T, ctx context.Context) {
	r.cluster.WaitUntilRunning(t, ctx)

	_, err := r.cluster.Client(t, ctx).ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "restartall",
		Job: &schedulerv1pb.Job{
			Schedule: new("@every 1s"),
		},
		Metadata: &schedulerv1pb.JobMetadata{
			AppId: "testapp", Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	r.cluster.SchedulerN(t, 0).WaitJobKeyCount(t, ctx, "restartall", func(n int) bool { return n == 1 })

	// Stop the whole cluster at once, gracefully so the leadership leases are
	// revoked rather than left to expire.
	var wg sync.WaitGroup
	wg.Add(3)
	for i := range 3 {
		go func(i int) {
			defer wg.Done()
			r.cluster.SchedulerN(t, i).Cleanup(t)
		}(i)
	}
	wg.Wait()

	// The exec pipe of a stopped process is closed, so restarted members need
	// fresh instances (with the same identity, ports, initial cluster and data
	// dir) for their logs to be captured.
	lines := make([]*logline.LogLine, 3)
	schedulers := make([]*scheduler.Scheduler, 3)
	for i := range 3 {
		old := r.cluster.SchedulerN(t, i)
		lines[i] = logline.New(t, logline.WithCaptureAll())
		lines[i].Run(t, ctx)
		t.Cleanup(func() { lines[i].Cleanup(t) })

		schedulers[i] = scheduler.New(t,
			scheduler.WithID(old.ID()),
			scheduler.WithPort(old.Port()),
			scheduler.WithHealthzPort(old.HealthzPort()),
			scheduler.WithMetricsPort(old.MetricsPort()),
			scheduler.WithEtcdClientPort(old.EtcdClientPort()),
			scheduler.WithInitialCluster(old.InitialCluster()),
			scheduler.WithDataDir(old.DataDir()),
			scheduler.WithLogLineStdout(lines[i]),
			scheduler.WithExecOptions(exec.WithStderr(lines[i].Stderr())),
		)
	}

	for i := range 3 {
		schedulers[i].Run(t, ctx)
		t.Cleanup(func() { schedulers[i].Cleanup(t) })
	}

	for i := range 3 {
		schedulers[i].WaitUntilRunning(t, ctx)
	}
	schedulers[0].WaitUntilLeadership(t, ctx, 3)

	// Each member must have started its server exactly once, and must not
	// rebuild it once the cluster is healthy. A second "listening on" line, a
	// recreate log or an etcd shutdown are the teardown/rebuild loop.
	rebuilding := func() bool {
		for i := range 3 {
			buf := lines[i].StdoutBuffer()
			if bytes.Count(buf, []byte("Dapr Scheduler listening on")) != 1 ||
				bytes.Contains(buf, []byte("Scheduler server failed, recreating")) ||
				bytes.Contains(buf, []byte("Scheduler cron exited unexpectedly")) ||
				bytes.Contains(buf, []byte("Etcd shut down")) {
				return true
			}
		}
		return false
	}
	require.False(t, rebuilding())
	assert.Never(t, rebuilding, 10*time.Second, 100*time.Millisecond)

	// The job survived the restart and still triggers.
	schedulers[0].WaitJobKeyCount(t, ctx, "restartall", func(n int) bool { return n == 1 })
	triggered := make(chan string, 3)
	for i := range 3 {
		ch := schedulers[i].WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
			AppId: "testapp", Namespace: "default",
		})
		go func() {
			select {
			case name := <-ch:
				triggered <- name
			case <-ctx.Done():
			}
		}()
	}
	select {
	case name := <-triggered:
		assert.Equal(t, "restartall", name)
	case <-time.After(20 * time.Second):
		require.Fail(t, "job did not trigger after the cluster restart")
	}

	_, err = schedulers[0].Client(t, ctx).ScheduleJob(ctx, schedulers[0].JobNowJob("post-restart", "default", "testapp"))
	require.NoError(t, err)
	_, err = schedulers[0].Client(t, ctx).DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
		Name: "restartall",
		Metadata: &schedulerv1pb.JobMetadata{
			AppId: "testapp", Namespace: "default",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	schedulers[0].WaitJobKeyCount(t, ctx, "restartall", func(n int) bool { return n == 0 })
}
