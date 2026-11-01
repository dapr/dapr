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

package api

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(leaderrestart))
}

// leaderrestart verifies that the surviving schedulers keep triggering the
// jobs in their partitions after the etcd raft leader is killed and restarted
// on the same ID and data dir, and that the restarted member resumes its own
// partition once it regains leadership.
type leaderrestart struct {
	cluster *cluster.Cluster

	lock   sync.Mutex
	counts map[string]int
	owner  map[string]int
}

func (l *leaderrestart) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	l.counts = make(map[string]int)
	l.owner = make(map[string]int)
	l.cluster = cluster.New(t, cluster.WithCount(3))

	return []framework.Option{
		framework.WithProcesses(l.cluster),
	}
}

func (l *leaderrestart) Run(t *testing.T, ctx context.Context) {
	l.cluster.WaitUntilRunning(t, ctx)

	for i := range 3 {
		l.watch(t, ctx, i)
	}

	// Jobs are assigned to partitions by a random id, so use enough of them that
	// every member owns at least one.
	const jobs = 30
	client := l.cluster.Client(t, ctx)
	for i := range jobs {
		_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name: fmt.Sprintf("job-%d", i),
			Job: &schedulerv1.Job{
				Schedule: new("@every 1s"),
				DueTime:  new(time.Now().Format(time.RFC3339)),
			},
			Metadata: &schedulerv1.JobMetadata{
				AppId: "testapp", Namespace: "default",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Job{
						Job: new(schedulerv1.TargetJob),
					},
				},
			},
		})
		require.NoError(t, err)
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, l.snapshot(), jobs)
	}, 10*time.Second, 10*time.Millisecond)

	leader := l.raftLeader(t, ctx)
	before := l.snapshot()
	var survivorJobs []string
	owners := make(map[int]int)
	l.lock.Lock()
	for name, owner := range l.owner {
		owners[owner]++
		if owner != leader {
			survivorJobs = append(survivorJobs, name)
		}
	}
	l.lock.Unlock()
	require.Len(t, owners, 3, "every member must own at least one job: %v", l.owner)

	survivor := l.cluster.SchedulerN(t, (leader+1)%3)
	leaderKey := "dapr/leadership/" + l.cluster.SchedulerN(t, leader).ID()
	resp, err := survivor.ETCDClient(t, ctx).Get(ctx, leaderKey)
	require.NoError(t, err)
	require.Len(t, resp.Kvs, 1)
	oldKeyRev := resp.Kvs[0].CreateRevision

	l.cluster.SchedulerN(t, leader).Restart(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		now := l.snapshot()
		for _, name := range survivorJobs {
			assert.GreaterOrEqual(c, now[name], before[name]+5, "survivor job %s stopped triggering", name)
		}
	}, 20*time.Second, 10*time.Millisecond)

	// The killed process could not revoke its leadership lease (20s TTL), so
	// the restarted member blocks in the elector until it expires. Wait for it
	// to write its own key rather than for the key to exist.
	l.cluster.SchedulerN(t, leader).WaitUntilRunning(t, ctx)
	l.watch(t, ctx, leader)
	etcd := survivor.ETCDClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		gctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		resp, err := etcd.Get(gctx, leaderKey)
		if assert.NoError(c, err) && assert.Len(c, resp.Kvs, 1) {
			assert.NotEqual(c, oldKeyRev, resp.Kvs[0].CreateRevision, "restarted member has not regained leadership")
		}
	}, 35*time.Second, 10*time.Millisecond)

	before = l.snapshot()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		now := l.snapshot()
		for name := range before {
			assert.GreaterOrEqual(c, now[name], before[name]+2, "job %s did not resume after the leader restart", name)
		}
	}, 10*time.Second, 10*time.Millisecond)
}

// watch opens a WatchJobs stream to member n, acknowledging every trigger as
// successful and recording it. A stream to a killed member ends with an error
// which is expected and ignored.
func (l *leaderrestart) watch(t *testing.T, ctx context.Context, n int) {
	t.Helper()

	stream, err := l.cluster.ClientN(t, ctx, n).WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId: "testapp", Namespace: "default",
			},
		},
	}))

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			l.lock.Lock()
			l.counts[resp.GetName()]++
			l.owner[resp.GetName()] = n
			l.lock.Unlock()
			if stream.Send(&schedulerv1.WatchJobsRequest{
				WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
					Result: &schedulerv1.WatchJobsRequestResult{
						Id:     resp.GetId(),
						Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
					},
				},
			}) != nil {
				return
			}
		}
	}()
}

func (l *leaderrestart) snapshot() map[string]int {
	l.lock.Lock()
	defer l.lock.Unlock()
	return maps.Clone(l.counts)
}

func (l *leaderrestart) raftLeader(t *testing.T, ctx context.Context) int {
	t.Helper()

	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for i := range 3 {
		cl := l.cluster.SchedulerN(t, i).ETCDClient(t, ctx)
		st, err := cl.Status(sctx, cl.Endpoints()[0])
		require.NoError(t, err)
		if st.Leader == st.Header.MemberId {
			return i
		}
	}
	require.Fail(t, "no raft leader found")
	return -1
}
