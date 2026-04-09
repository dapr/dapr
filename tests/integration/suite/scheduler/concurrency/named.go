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
	suite.Register(new(named))
}

// named tests per-name concurrency limits at the scheduler level. Jobs with
// concurrency_key "slow" are limited to 1 concurrent, while jobs with
// concurrency_key "fast" have no per-name limit.
type named struct {
	scheduler *scheduler.Scheduler
}

func (n *named) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *named) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)

	client := n.scheduler.Client(t, ctx)

	// Schedule 2 "slow" jobs and 2 "fast" jobs.
	for _, name := range []string{"slow-1", "slow-2"} {
		req := n.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		req.Metadata.ConcurrencyKey = ptr.Of("slow")
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}
	for _, name := range []string{"fast-1", "fast-2"} {
		req := n.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		req.Metadata.ConcurrencyKey = ptr.Of("fast")
		_, err := client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}

	// Open stream with per-name limit: "slow" = 1 concurrent.
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
					{Group: "mytype", Name: ptr.Of("slow"), MaxConcurrent: 1},
				},
			},
		},
	}))

	// We expect 3 triggers: 2 fast (unlimited) + 1 slow (limited to 1).
	type recvResult struct {
		resp *schedulerv1.WatchJobsResponse
		err  error
	}
	recvCh := make(chan recvResult, 4)
	go func() {
		for {
			resp, err := stream.Recv()
			recvCh <- recvResult{resp, err}
			if err != nil {
				return
			}
		}
	}()

	received := make(map[string]*schedulerv1.WatchJobsResponse)
	for range 3 {
		select {
		case r := <-recvCh:
			require.NoError(t, r.err)
			received[r.resp.GetName()] = r.resp
			if r.resp.GetMetadata().GetConcurrencyKey() == "fast" {
				require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
					WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
						Result: &schedulerv1.WatchJobsRequestResult{
							Id:     r.resp.GetId(),
							Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
						},
					},
				}))
			}
		case <-time.After(time.Second * 10):
			t.Fatalf("timed out, only received %d triggers", len(received))
		}
	}

	assert.Contains(t, received, "fast-1")
	assert.Contains(t, received, "fast-2")
	// Exactly one slow job should have been delivered.
	slowCount := 0
	var slowResp *schedulerv1.WatchJobsResponse
	for name, resp := range received {
		if name == "slow-1" || name == "slow-2" {
			slowCount++
			slowResp = resp
		}
	}
	assert.Equal(t, 1, slowCount, "only 1 slow job should be in-flight")

	// No 4th trigger should arrive (the 2nd slow is held).
	select {
	case <-recvCh:
		t.Fatal("received 4th trigger while slow limit should hold it back")
	case <-time.After(time.Second * 3):
	}

	// Ack the slow job, the 2nd slow should now arrive.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     slowResp.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	select {
	case r := <-recvCh:
		require.NoError(t, r.err)
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for 2nd slow trigger after ack")
	}
}
