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
	suite.Register(new(overwritten))
}

// overwritten asserts a pull trigger its overwritten job replaces frees its
// concurrency slot and inflight count: the sidecar's stream drops, it
// reconnects without listing the reminder, and the job fires again before
// the reattach window ends, so the first delivery is never answered.
type overwritten struct {
	scheduler *scheduler.Scheduler
}

func (o *overwritten) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(o.scheduler),
	}
}

func (o *overwritten) Run(t *testing.T, ctx context.Context) {
	o.scheduler.WaitUntilRunning(t, ctx)

	limited := func() *schedulerv1.WatchJobsRequestInitial {
		initial := sidecar("addr-1", free(2))
		initial.ConcurrencyLimits = []*schedulerv1.ConcurrencyLimit{{
			Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
			MaxConcurrent: 2,
		}}
		return initial
	}
	client := o.scheduler.Client(t, ctx)
	schedule := func(name, actorID string, overwrite bool) {
		t.Helper()
		req := o.scheduler.JobNowPull(name, namespace, appID, actorType, actorID)
		req.Overwrite = overwrite
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}

	stream := o.scheduler.WatchJobsStream(t, ctx, limited())
	o.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	schedule("run", "instance1::0", false)
	require.Equal(t, "run", stream.Recv().GetName())

	// The sidecar comes back without the reminder, as after a restart.
	stream.Close()
	o.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)
	stream = o.scheduler.WatchJobsStream(t, ctx, limited())
	o.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	schedule("run", "instance1::0", true)
	again := stream.Recv()
	require.Equal(t, "run", again.GetName())
	stream.Result(again.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, o.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_inflight", "app_id:"+appID), 0)
		// The cron counts the first delivery's trigger as in flight until it
		// is answered, even though it drops the answer for an old version.
		inFlight, _ := o.scheduler.ScalingGauges(c, ctx, namespace, appID)
		assert.InDelta(c, 0.0, inFlight, 0)
	}, time.Second*10, time.Millisecond*10)

	// Both concurrency slots are free again.
	stream.PullSlots(actorType, 2)
	schedule("second", "instance2::0", false)
	schedule("third", "instance3::0", false)
	assert.ElementsMatch(t, []string{"second", "third"}, []string{stream.Recv().GetName(), stream.Recv().GetName()}, "the replaced trigger's concurrency slot must be freed")
	stream.NoRecv(time.Second)
}
