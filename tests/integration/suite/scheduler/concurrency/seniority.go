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

package concurrency

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
	suite.Register(new(seniority))
}

// seniority verifies the pending-queue drain preserves delivery seniority.
type seniority struct {
	scheduler *scheduler.Scheduler
}

func (s *seniority) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *seniority) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)

	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:      "myapp",
				Namespace:  "default",
				ActorTypes: []string{"mytype"},
				AcceptJobTypes: []schedulerv1.JobTargetType{
					schedulerv1.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
				ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{
					{
						Target: &schedulerv1.ConcurrencyLimit_Actor{
							Actor: &schedulerv1.ConcurrencyLimitActor{Type: "mytype"},
						},
						MaxConcurrent: 2,
					},
					{
						Target: &schedulerv1.ConcurrencyLimit_Actor{
							Actor: &schedulerv1.ConcurrencyLimitActor{Type: "mytype"},
						},
						Name:          ptr.Of("hot"),
						MaxConcurrent: 1,
					},
				},
			},
		},
	}))

	type recvResult struct {
		resp *schedulerv1.WatchJobsResponse
		err  error
	}
	recvCh := make(chan recvResult, 10)
	go func() {
		for {
			resp, rerr := stream.Recv()
			recvCh <- recvResult{resp, rerr}
			if rerr != nil {
				return
			}
		}
	}()

	schedule := func(name, key string) {
		req := s.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		if key != "" {
			req.Metadata.ConcurrencyKey = ptr.Of(key)
		}
		_, serr := client.ScheduleJob(ctx, req)
		require.NoError(t, serr)
	}

	recv := func(msg string) *schedulerv1.WatchJobsResponse {
		t.Helper()
		select {
		case r := <-recvCh:
			require.NoError(t, r.err)
			return r.resp
		case <-time.After(time.Second * 10):
			t.Fatal("timed out waiting for trigger: " + msg)
			return nil
		}
	}

	ack := func(resp *schedulerv1.WatchJobsResponse) {
		require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
				Result: &schedulerv1.WatchJobsRequestResult{
					Id:     resp.GetId(),
					Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
	}

	waitPending := func(n int) {
		t.Helper()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			ms := s.scheduler.Metrics(c, ctx).MatchMetric(
				"dapr_scheduler_concurrency_pending", "concurrency_key:mytype")
			if assert.Len(c, ms, 1) {
				assert.Equal(c, n, int(ms[0].Value))
			}
		}, time.Second*10, time.Millisecond*10)
	}

	schedule("hold-hot", "hot")
	holdHot := recv("hold-hot")
	require.Equal(t, "hold-hot", holdHot.GetName())
	schedule("hold-type", "")
	holdType := recv("hold-type")
	require.Equal(t, "hold-type", holdType.GetName())

	schedule("w1", "hot")
	waitPending(1)
	schedule("f1", "")
	waitPending(2)
	schedule("w2", "hot")
	waitPending(3)

	ack(holdType)
	f1 := recv("f1 after hold-type ack")
	require.Equal(t, "f1", f1.GetName())

	ack(holdHot)
	ack(f1)
	first := recv("first hot waiter")
	assert.Equal(t, "w1", first.GetName(),
		"gate-blocked elder lost its place in the pending queue to a fresher arrival")

	ack(first)
	schedule("f2", "")
	f2 := recv("f2")
	require.Equal(t, "f2", f2.GetName())
	ack(f2)
	second := recv("second hot waiter")
	assert.Equal(t, "w2", second.GetName())
	ack(second)
}
