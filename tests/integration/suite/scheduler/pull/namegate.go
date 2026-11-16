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
	suite.Register(new(namegate))
}

// namegate asserts that a per-name concurrency limit at its maximum does not
// hold back a workflow instance's other parked activities: a freed slot goes
// to the instance's next activity of another name.
type namegate struct {
	scheduler *scheduler.Scheduler
}

func (n *namegate) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *namegate) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)

	client := n.scheduler.Client(t, ctx)
	stream := n.scheduler.WatchJobsStream(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:          appID,
		Namespace:      namespace,
		AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:     []string{actorType},
		PullSlots:      []*schedulerv1.PullSlots{{ActorType: actorType, Free: 1}},
		ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{{
			Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
			Name:          new("slow"),
			MaxConcurrent: 1,
		}},
	})
	n.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	// One workflow instance runs a Slow activity, which takes the only slot
	// and Slow's whole limit.
	schedule := func(name, taskID, activity string) {
		t.Helper()
		req := n.scheduler.JobNowPull(name, namespace, appID, actorType, "wf::"+taskID+"::0")
		req.Metadata.ConcurrencyKey = new(activity)
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}
	schedule("slow-1", "1", "slow")
	require.Equal(t, "slow-1", stream.Recv().GetName())

	// It then parks a second Slow activity, and after it a Fast one.
	backlog := func(activity string) float64 {
		return n.scheduler.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_backlog", "app_id:"+appID, "activity_name:"+activity)
	}
	schedule("slow-2", "2", "slow")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, backlog("slow"), 0)
	}, time.Second*10, time.Millisecond*10)
	schedule("fast-1", "3", "fast")
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, backlog("fast"), 0)
	}, time.Second*10, time.Millisecond*10)
	stream.NoRecv(time.Second)

	// A slot frees while Slow is still at its limit: Fast takes it.
	stream.PullSlots(actorType, 1)
	assert.Equal(t, "fast-1", stream.Recv().GetName(), "the full Slow limit must not hold back Fast")
	stream.NoRecv(time.Second * 2)
}
