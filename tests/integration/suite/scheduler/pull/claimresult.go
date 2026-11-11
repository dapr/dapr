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
	suite.Register(new(claimresult))
}

// claimresult asserts the result a sidecar sends for a reminder it lists as
// running, before the cron fires that reminder again, outlives the sidecar's
// last stream and resolves the trigger without a delivery, and is forgotten
// after the reattach window so a later reminder of the same name still runs.
type claimresult struct {
	scheduler *scheduler.Scheduler
}

func (s *claimresult) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *claimresult) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)
	client := s.scheduler.Client(t, ctx)
	schedule := func(name, actorID string) {
		t.Helper()
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowPull(name, namespace, appID, actorType, actorID))
		require.NoError(t, err)
	}

	kept, stale := reminder("kept::0", "kept"), reminder("stale::0", "stale")
	claimant := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0), kept, stale))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	claimant.ResultFor(kept, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	claimant.ResultFor(stale, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	sent := time.Now()
	claimant.Close()
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)

	schedule("kept", "kept::0")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, s.scheduler.EtcdJobs(t, ctx), "resolved with the result its sidecar sent")
	}, time.Second*10, time.Millisecond*10)

	spare := s.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(2)))
	s.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	spare.NoRecv(time.Until(sent.Add(time.Second * 31)))
	schedule("stale", "stale::0")
	assert.Equal(t, "stale", spare.Recv().GetName(), "a result is not kept past the reattach window")
}
