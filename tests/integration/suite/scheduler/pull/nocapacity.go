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

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nocapacity))
}

// nocapacity asserts a NO_CAPACITY result parks a pull trigger again until
// the sidecar reports a free slot, without applying the failure policy, while
// the same result for a regular trigger resolves it as failed.
type nocapacity struct {
	scheduler *scheduler.Scheduler
}

func (n *nocapacity) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *nocapacity) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)

	client := n.scheduler.Client(t, ctx)
	stream := n.scheduler.WatchJobsStream(t, ctx, &schedulerv1.WatchJobsRequestInitial{
		AppId:          appID,
		Namespace:      namespace,
		AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
		ActorTypes:     []string{actorType},
		PullSlots:      []*schedulerv1.PullSlots{{ActorType: actorType, Free: 1}},
	})
	n.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	drop := &corev1.JobFailurePolicy{
		Policy: &corev1.JobFailurePolicy_Drop{Drop: new(corev1.JobFailurePolicyDrop)},
	}

	job := n.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0")
	job.Job.FailurePolicy = drop
	job.Metadata.ConcurrencyKey = new("act")
	_, err := client.ScheduleJob(ctx, job)
	require.NoError(t, err)
	resp := stream.Recv()
	require.Equal(t, "pull-1", resp.GetName())
	stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_NO_CAPACITY)

	refused := func(c assert.TestingT) float64 {
		return n.scheduler.Metrics(c, ctx).SumWithLabels("dapr_scheduler_workflow_activity_refused_total", "app_id:"+appID, "activity_name:act")
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, refused(c), 0)
	}, time.Second*10, time.Millisecond*10)
	stream.NoRecv(time.Second * 3)
	assert.Len(t, n.scheduler.EtcdJobs(t, ctx), 1, "a refused pull trigger must not be dropped by its failure policy")

	stream.PullSlots(actorType, 1)
	resp = stream.Recv()
	require.Equal(t, "pull-1", resp.GetName())
	stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, n.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	job = n.scheduler.JobNowActor("regular-1", namespace, appID, actorType, "instance2::0")
	job.Job.FailurePolicy = drop
	_, err = client.ScheduleJob(ctx, job)
	require.NoError(t, err)
	resp = stream.Recv()
	require.Equal(t, "regular-1", resp.GetName())
	stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_NO_CAPACITY)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, n.scheduler.EtcdJobs(t, ctx), "NO_CAPACITY for a regular trigger resolves it as failed")
	}, time.Second*10, time.Millisecond*10)
	stream.NoRecv(time.Second)
	assert.InDelta(t, 1.0, refused(t), 0, "a regular trigger's NO_CAPACITY is a failure, not a refusal")
}
