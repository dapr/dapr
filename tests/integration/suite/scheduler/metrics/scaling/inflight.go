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
	suite.Register(new(inflight))
}

// inflight tests that jobs sent to a busy sidecar, or held in a concurrency
// gate on its behalf, are counted in flight until answered, and never as
// undeliverable. With an activity concurrency limit, only that many reach the
// worker and the rest wait in the scheduler's gate.
type inflight struct {
	activityLimit int32
	scheduler     *scheduler.Scheduler
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	return i.setup(t, 0)
}

func (i *inflight) setup(t *testing.T, activityLimit int32) []framework.Option {
	i.activityLimit = activityLimit
	i.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(i.scheduler),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	const activityType = "dapr.internal.default.myapp.activity"

	i.scheduler.WaitUntilRunning(t, ctx)

	client := i.scheduler.Client(t, ctx)

	initial := &schedulerv1pb.WatchJobsRequestInitial{
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{activityType},
		AcceptJobTypes: []schedulerv1pb.JobTargetType{
			schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
		},
	}
	if i.activityLimit > 0 {
		initial.ConcurrencyLimits = []*schedulerv1pb.ConcurrencyLimit{{
			Target: &schedulerv1pb.ConcurrencyLimit_Actor{
				Actor: &schedulerv1pb.ConcurrencyLimitActor{Type: activityType},
			},
			MaxConcurrent: i.activityLimit,
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
	for n := range jobs {
		_, err = client.ScheduleJob(ctx, i.scheduler.JobNowActor("run-activity-"+strconv.Itoa(n), "default", "myapp", activityType, "act-"+strconv.Itoa(n)))
		require.NoError(t, err)
	}

	// With a limit, only that many reach the worker; the rest wait in the
	// gate, and every one of them is in flight.
	sent := jobs
	if i.activityLimit > 0 {
		sent = int(i.activityLimit)
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
		inFlight, undeliverable := i.scheduler.ScalingGauges(c, ctx, "default", "myapp")
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
		inFlight, undeliverable := i.scheduler.ScalingGauges(c, ctx, "default", "myapp")
		assert.InDelta(c, 0.0, inFlight, 0)
		assert.InDelta(c, 0.0, undeliverable, 0)
	}, time.Second*20, time.Millisecond*100)
}
