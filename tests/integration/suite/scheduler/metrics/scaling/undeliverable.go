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

package scaling

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(undeliverable))
}

// undeliverable tests that jobs no sidecar can take are counted as
// undeliverable for the app that will run them, and cleared once a sidecar
// connects.
type undeliverable struct {
	scheduler *scheduler.Scheduler
}

func (u *undeliverable) Setup(t *testing.T) []framework.Option {
	u.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(u.scheduler),
	}
}

func (u *undeliverable) Run(t *testing.T, ctx context.Context) {
	const workflowType = "dapr.internal.default.myapp.workflow"

	u.scheduler.WaitUntilRunning(t, ctx)

	client := u.scheduler.Client(t, ctx)
	for i := range 3 {
		_, err := client.ScheduleJob(ctx, u.scheduler.JobNowJob("job-"+strconv.Itoa(i), "default", "myapp"))
		require.NoError(t, err)
	}
	for i := range 2 {
		_, err := client.ScheduleJob(ctx, u.scheduler.JobNowActor("start-"+strconv.Itoa(i), "default", "myapp", workflowType, "wf-"+strconv.Itoa(i)))
		require.NoError(t, err)
	}
	// A user actor reminder is not counted.
	_, err := client.ScheduleJob(ctx, u.scheduler.JobNowActor("reminder", "default", "myapp", "myactortype", "id"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		inFlight, undeliverable := u.scheduler.ScalingGauges(c, ctx, "default", "myapp")
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 5.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)

	triggered := u.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{workflowType},
		AcceptJobTypes: []schedulerv1pb.JobTargetType{
			schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB,
			schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
		},
	})
	for range 5 {
		select {
		case <-triggered:
		case <-time.After(time.Second * 20):
			require.Fail(t, "timed out waiting for staged jobs to be delivered")
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		inFlight, undeliverable := u.scheduler.ScalingGauges(c, ctx, "default", "myapp")
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 0.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)
}
