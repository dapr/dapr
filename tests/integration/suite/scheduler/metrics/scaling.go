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

package metrics

import (
	"context"
	"strconv"
	"strings"
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
	suite.Register(new(scalingUndeliverable))
	suite.Register(new(scalingInFlight))
	suite.Register(new(scalingInFlightLimit))
}

const (
	scalingWorkflowType = "dapr.internal.default.myapp.workflow"
	scalingActivityType = "dapr.internal.default.myapp.activity"

	inFlightMetric      = "dapr_scheduler_inflight_jobs"
	undeliverableMetric = "dapr_scheduler_undeliverable_jobs"
)

// scalingGauges returns the scaling gauges for myapp, and fails if the
// endpoint serves anything else.
func scalingGauges(c *assert.CollectT, sched *scheduler.Scheduler, ctx context.Context) (inFlight, undeliverable float64) {
	all := sched.ScalingMetrics(c, ctx).All()
	for name := range all {
		metric, _, _ := strings.Cut(name, "|")
		assert.Contains(c, []string{inFlightMetric, undeliverableMetric}, metric)
	}
	return all[inFlightMetric+"|app_id:myapp|namespace:default"],
		all[undeliverableMetric+"|app_id:myapp|namespace:default"]
}

// scalingUndeliverable tests that jobs no sidecar can take are counted as
// undeliverable for the app that will run them, and cleared once a sidecar
// connects.
type scalingUndeliverable struct {
	scheduler *scheduler.Scheduler
}

func (s *scalingUndeliverable) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *scalingUndeliverable) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)
	for i := range 3 {
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowJob("job-"+strconv.Itoa(i), "default", "myapp"))
		require.NoError(t, err)
	}
	for i := range 2 {
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowActor("start-"+strconv.Itoa(i), "default", "myapp", scalingWorkflowType, "wf-"+strconv.Itoa(i)))
		require.NoError(t, err)
	}
	// A user actor reminder is not counted.
	_, err := client.ScheduleJob(ctx, s.scheduler.JobNowActor("reminder", "default", "myapp", "myactortype", "id"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		inFlight, undeliverable := scalingGauges(c, s.scheduler, ctx)
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 5.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)

	triggered := s.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{scalingWorkflowType},
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
		inFlight, undeliverable := scalingGauges(c, s.scheduler, ctx)
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 0.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)
}

// scalingInFlight tests that jobs sent to a busy sidecar, or held in a
// concurrency gate on its behalf, are counted in flight until answered, and
// never as undeliverable. With an activity concurrency limit, only that many
// reach the worker and the rest wait in the scheduler's gate.
type scalingInFlight struct {
	activityLimit int32
	scheduler     *scheduler.Scheduler
}

// scalingInFlightLimit runs scalingInFlight with an activity concurrency
// limit of 2.
type scalingInFlightLimit struct{ scalingInFlight }

func (s *scalingInFlight) Setup(t *testing.T) []framework.Option {
	return s.setup(t, 0)
}

func (s *scalingInFlightLimit) Setup(t *testing.T) []framework.Option {
	return s.setup(t, 2)
}

func (s *scalingInFlight) setup(t *testing.T, activityLimit int32) []framework.Option {
	s.activityLimit = activityLimit
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *scalingInFlight) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)

	initial := &schedulerv1pb.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{scalingActivityType},
		AcceptJobTypes: []schedulerv1pb.JobTargetType{
			schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
		},
	}
	if s.activityLimit > 0 {
		initial.ConcurrencyLimits = []*schedulerv1pb.ConcurrencyLimit{{
			Target: &schedulerv1pb.ConcurrencyLimit_Actor{
				Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: scalingActivityType},
			},
			MaxConcurrent: s.activityLimit,
		}}
	}

	// A worker that takes jobs and answers none of them until told to.
	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: initial},
	}))
	recvCh := make(chan *schedulerv1pb.WatchJobsResponse, 10)
	go func() {
		for {
			resp, rerr := stream.Recv()
			if rerr != nil {
				return
			}
			recvCh <- resp
		}
	}()

	const jobs = 6
	for i := range jobs {
		_, err = client.ScheduleJob(ctx, s.scheduler.JobNowActor("run-activity-"+strconv.Itoa(i), "default", "myapp", scalingActivityType, "act-"+strconv.Itoa(i)))
		require.NoError(t, err)
	}

	// With a limit, only that many reach the worker; the rest wait in the
	// gate, and every one of them is in flight.
	sent := jobs
	if s.activityLimit > 0 {
		sent = int(s.activityLimit)
	}
	var received []*schedulerv1pb.WatchJobsResponse
	for range sent {
		select {
		case resp := <-recvCh:
			received = append(received, resp)
		case <-time.After(time.Second * 20):
			require.Fail(t, "timed out waiting for triggers")
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		inFlight, undeliverable := scalingGauges(c, s.scheduler, ctx)
		assert.InDelta(c, float64(jobs), inFlight, 0)
		assert.InDelta(c, 0.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)

	// Answer every trigger, including those released from the gate as
	// earlier ones are answered.
	for range jobs {
		if len(received) == 0 {
			select {
			case resp := <-recvCh:
				received = append(received, resp)
			case <-time.After(time.Second * 20):
				require.Fail(t, "timed out waiting for gated triggers")
			}
		}
		require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id: received[0].GetId(), Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
		received = received[1:]
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		inFlight, undeliverable := scalingGauges(c, s.scheduler, ctx)
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 0.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)
}
