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
	"context"
	"strconv"
	"strings"
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
	suite.Register(new(etcdleader))
}

// etcdleader tests that a scheduler which is the etcd raft leader hands off
// that leadership before stopping its cron, so the survivors' re-election
// writes do not stall on the etcd request timeout.
type etcdleader struct {
	cluster *cluster.Cluster
	logs    [3]*logline.LogLine
}

func (e *etcdleader) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	opts := make([]cluster.Option, 0, 2+len(e.logs))
	opts = append(opts,
		cluster.WithCount(3),
		cluster.WithSchedulerOptions(scheduler.WithPlacementEnabled(true)),
	)
	for n := range e.logs {
		e.logs[n] = logline.New(t, logline.WithCaptureAll())
		opts = append(opts, cluster.WithSchedulerNOptions(uint32(n), scheduler.WithExecOptions(
			exec.WithStdout(e.logs[n].Stdout()),
			exec.WithStderr(e.logs[n].Stderr()),
		)))
	}
	e.cluster = cluster.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(e.logs[0], e.logs[1], e.logs[2], e.cluster),
	}
}

func (e *etcdleader) Run(t *testing.T, ctx context.Context) {
	e.cluster.WaitUntilRunning(t, ctx)

	leader := -1
	var leaderID, term uint64
	for n := range 3 {
		endpoint := "127.0.0.1:" + strconv.Itoa(e.cluster.EtcdClientPortN(t, n))
		status, err := e.cluster.SchedulerN(t, n).ETCDClient(t, ctx).Status(ctx, endpoint)
		require.NoError(t, err)
		if status.Header.MemberId == status.Leader {
			leader, leaderID, term = n, status.Leader, status.RaftTerm
		}
	}
	require.NotEqual(t, -1, leader)

	var survivors []int
	for n := range 3 {
		if n != leader {
			survivors = append(survivors, n)
		}
	}

	// hosts returns the addresses and the leader addresses a scheduler
	// currently broadcasts.
	hosts := func(c *assert.CollectT, n int) ([]string, []string) {
		stream, err := e.cluster.ClientN(t, ctx, n).WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
		require.NoError(c, err)
		//nolint:errcheck
		defer stream.CloseSend()
		resp, err := stream.Recv()
		require.NoError(c, err)
		addrs := make([]string, 0, len(resp.GetHosts()))
		var leaders []string
		for _, host := range resp.GetHosts() {
			addrs = append(addrs, host.GetAddress())
			if host.GetLeader() {
				leaders = append(leaders, host.GetAddress())
			}
		}
		return addrs, leaders
	}

	// A placement capable sidecar on each survivor, so they advertise a
	// placement leader, and later receive the jobs scheduled on them.
	initial := &schedulerv1pb.WatchJobsRequestInitial{
		AppId:                      "app",
		Namespace:                  "default",
		SupportsSchedulerPlacement: true,
	}
	triggered := make([]<-chan string, 2)
	for i, n := range survivors {
		triggered[i] = e.cluster.SchedulerN(t, n).WatchJobsSuccess(t, ctx, initial)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			addrs, leaders := hosts(c, n)
			assert.Len(c, addrs, 3)
			assert.Len(c, leaders, 1)
		}, 20*time.Second, 10*time.Millisecond)
	}

	start := time.Now()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		e.cluster.SchedulerN(t, leader).Cleanup(t)
	}()

	// Both survivors publish the same two-host table with one leader, well
	// inside the 7s etcd request timeout.
	leaders := make([]string, 2)
	for i, n := range survivors {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			addrs, leaderAddrs := hosts(c, n)
			assert.ElementsMatch(c, []string{
				e.cluster.Addresses()[survivors[0]],
				e.cluster.Addresses()[survivors[1]],
			}, addrs)
			if assert.Len(c, leaderAddrs, 1) {
				leaders[i] = leaderAddrs[0]
			}
		}, time.Until(start.Add(5*time.Second)), time.Millisecond*10)
	}
	require.Equal(t, leaders[0], leaders[1])

	select {
	case <-stopped:
	case <-time.After(20 * time.Second):
		require.FailNow(t, "timed out waiting for etcd leader scheduler to stop")
	}

	// The stopped scheduler transferred etcd leadership before its cron shut
	// down, so the survivors' writes never raced the transfer.
	logs := string(e.logs[leader].StdoutBuffer())
	require.Contains(t, logs, "leadership transfer finished")
	require.Contains(t, logs, "cron instance shutdown")
	require.Less(t,
		strings.Index(logs, "leadership transfer finished"),
		strings.Index(logs, "cron instance shutdown"),
		"etcd leadership transferred after cron shut down")

	status, err := e.cluster.SchedulerN(t, survivors[0]).ETCDClient(t, ctx).
		Status(ctx, "127.0.0.1:"+strconv.Itoa(e.cluster.EtcdClientPortN(t, survivors[0])))
	require.NoError(t, err)
	assert.NotEqual(t, leaderID, status.Leader)
	assert.Greater(t, status.RaftTerm, term)

	// The survivors' engines are running: a job scheduled on each fires.
	for i, n := range survivors {
		_, err = e.cluster.ClientN(t, ctx, n).ScheduleJob(ctx, e.cluster.SchedulerN(t, n).JobNowJob("job"+strconv.Itoa(i), "default", "app"))
		require.NoError(t, err)
	}
	var got []string
	for len(got) < 2 {
		select {
		case name := <-triggered[0]:
			got = append(got, name)
		case name := <-triggered[1]:
			got = append(got, name)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "timed out waiting for jobs to trigger", "got %v", got)
		}
	}
	assert.ElementsMatch(t, []string{"job0", "job1"}, got)
}
