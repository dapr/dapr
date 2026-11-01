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
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(reattach))
}

// reattach asserts a pull trigger whose stream closed is held for its sidecar
// instead of being delivered again: a new stream from the same sidecar lists
// it as running and resolves it by reference, while a result by reference
// from another sidecar is dropped.
type reattach struct {
	scheduler *scheduler.Scheduler
}

func (r *reattach) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *reattach) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	initial := func(addr string, slots []*schedulerv1.PullSlots, inflight ...*schedulerv1.ActorReminderRef) *schedulerv1.WatchJobsRequestInitial {
		return &schedulerv1.WatchJobsRequestInitial{
			AppId:             appID,
			Namespace:         namespace,
			AcceptJobTypes:    []schedulerv1.JobTargetType{schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER},
			ActorTypes:        []string{actorType},
			ActorAddress:      ptr.Of(addr),
			PullSlots:         slots,
			InflightReminders: inflight,
		}
	}

	first := r.scheduler.WatchJobsStream(t, ctx, initial("addr-1", []*schedulerv1.PullSlots{{ActorType: actorType, Free: 1}}))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	_, err := r.scheduler.Client(t, ctx).ScheduleJob(ctx, r.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	require.Equal(t, "pull-1", first.Recv().GetName())

	first.Close()
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)

	ref := &schedulerv1.ActorReminderRef{ActorType: actorType, ActorId: "instance1::0", Name: "pull-1"}
	again := r.scheduler.WatchJobsStream(t, ctx, initial("addr-1", []*schedulerv1.PullSlots{{ActorType: actorType, Free: 0}}, ref))
	other := r.scheduler.WatchJobsStream(t, ctx, initial("addr-2", nil))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	again.NoRecv(time.Second * 2)

	other.ResultFor(ref, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	again.NoRecv(time.Second * 2)
	assert.Len(t, r.scheduler.EtcdJobs(t, ctx), 1, "a result from another sidecar must not resolve the trigger")

	again.ResultFor(ref, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
	again.NoRecv(time.Second)
	other.NoRecv(0)
}
