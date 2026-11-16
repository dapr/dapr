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
	suite.Register(new(marked))
}

// marked asserts only a delivery made against a free slot carries
// WatchJobsResponse.pull: a regular reminder is not marked, nor is a pull job
// that took regular routing, which still carries its stored JobMetadata.pull
// as it would from a scheduler without pull dispatch. A refused delivery is
// marked again when it is dispatched again.
type marked struct {
	scheduler *scheduler.Scheduler
}

func (m *marked) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(m.scheduler),
	}
}

func (m *marked) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)
	client := m.scheduler.Client(t, ctx)

	initial := func(slots []*schedulerv1.PullSlots) *schedulerv1.WatchJobsRequestInitial {
		return &schedulerv1.WatchJobsRequestInitial{
			AppId:          appID,
			Namespace:      namespace,
			AcceptJobTypes: []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
			ActorTypes:     []string{actorType},
			PullSlots:      slots,
		}
	}
	deliver := func(stream *scheduler.Stream, job *schedulerv1.ScheduleJobRequest) *schedulerv1.WatchJobsResponse {
		_, err := client.ScheduleJob(ctx, job)
		require.NoError(t, err)
		resp := stream.Recv()
		require.Equal(t, job.GetName(), resp.GetName())
		return resp
	}

	// No sidecar reports slots, so the pull job takes regular routing.
	stream := m.scheduler.WatchJobsStream(t, ctx, initial(nil))
	m.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	resp := deliver(stream, m.scheduler.JobNowPull("routed", namespace, appID, actorType, "instance1::0"))
	assert.True(t, resp.GetMetadata().GetPull(), "the stored flag passes through")
	assert.False(t, resp.GetPull(), "a regularly routed delivery is not marked")
	stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	stream.Close()
	m.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)

	stream = m.scheduler.WatchJobsStream(t, ctx, initial([]*schedulerv1.PullSlots{{ActorType: actorType, Free: 1}}))
	m.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	pull := deliver(stream, m.scheduler.JobNowPull("pull", namespace, appID, actorType, "instance2::0"))
	assert.True(t, pull.GetPull(), "a delivery against a free slot is marked")
	regular := deliver(stream, m.scheduler.JobNowActor("regular", namespace, appID, actorType, "instance3::0"))
	assert.False(t, regular.GetMetadata().GetPull())
	assert.False(t, regular.GetPull(), "a regular reminder is not marked")
	stream.Result(regular.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)

	stream.Result(pull.GetId(), schedulerv1.WatchJobsRequestResultStatus_NO_CAPACITY)
	stream.PullSlots(actorType, 1)
	resp = stream.Recv()
	require.Equal(t, "pull", resp.GetName())
	assert.True(t, resp.GetPull(), "a refused delivery is marked again when dispatched again")
	stream.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, m.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
}
