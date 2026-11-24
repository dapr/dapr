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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(parkedclaim))
}

// parkedclaim asserts a trigger parked for lack of a free slot goes to a
// sidecar that then connects listing its reminder as running, without using
// a slot, and not to another sidecar when one frees a slot.
type parkedclaim struct {
	scheduler *scheduler.Scheduler
}

func (s *parkedclaim) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *parkedclaim) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	other := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(0)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	schedule("pull-1", "instance1::0")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, s.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_backlog", "app_id:"+appID), 0)
	}, time.Second*10, time.Millisecond*10)

	running := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0), reminder("instance1::0", "pull-1")))
	resp := running.Recv()
	require.Equal(t, "pull-1", resp.GetName())
	assert.True(t, resp.GetPull())

	other.PullSlots(actorType, 1)
	other.NoRecv(time.Second)
}
