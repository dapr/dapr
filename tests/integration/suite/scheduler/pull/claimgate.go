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

package pull

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(claimgate))
}

// claimgate asserts the result a sidecar sends for a reminder it lists as
// running resolves that reminder's trigger even while the actor type's
// concurrency limit is full and another trigger waits for it.
type claimgate struct {
	scheduler *scheduler.Scheduler
}

func (s *claimgate) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *claimgate) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	claimed := reminder("claimed::1::0", "claimed")
	initial := sidecar("addr-1", free(3), claimed)
	initial.ConcurrencyLimits = []*schedulerv1.ConcurrencyLimit{{
		Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
		MaxConcurrent: 1,
	}}
	stream := s.scheduler.WatchJobsStream(t, ctx, initial)
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	schedule("running", "running::1::0")
	require.Equal(t, "running", stream.Recv().GetName())
	schedule("waiting", "waiting::1::0")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, s.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_backlog", "app_id:"+appID), 0)
	}, time.Second*10, time.Millisecond*10)

	schedule("claimed", "claimed::1::0")
	stream.NoRecv(time.Second)
	stream.ResultFor(claimed, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, s.scheduler.EtcdJobs(t, ctx), 2, "the claimed trigger resolves with its result, behind a full limit")
	}, time.Second*10, time.Millisecond*10)
	stream.NoRecv(time.Second)
}
