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
	suite.Register(new(releaseorder))
}

// releaseorder verifies that acknowledging a trigger which holds both the
// actor type gate and a per-name gate dispatches the next trigger waiting on
// that name. Waiting triggers queue on the type gate, so every gate the
// trigger held must be released before any is drained; otherwise the waiter
// fails to acquire the still-held per-name gate and stalls until an unrelated
// release, which never comes here.
type releaseorder struct {
	scheduler *scheduler.Scheduler
}

func (r *releaseorder) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *releaseorder) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	client := r.scheduler.Client(t, ctx)

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
						MaxConcurrent: 10,
					},
					{
						Target: &schedulerv1.ConcurrencyLimit_Actor{
							Actor: &schedulerv1.ConcurrencyLimitActor{Type: "mytype"},
						},
						Name:          ptr.Of("act"),
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

	const jobs = 10
	for i := range jobs {
		name := "act-" + string(rune('a'+i))
		req := r.scheduler.JobNowActor(name, "default", "myapp", "mytype", name)
		req.Metadata.ConcurrencyKey = ptr.Of("act")
		_, err = client.ScheduleJob(ctx, req)
		require.NoError(t, err)
	}

	// Each acknowledgement is the only release, so it must dispatch the next
	// waiter by itself.
	seen := make(map[string]bool, jobs)
	for i := range jobs {
		var resp *schedulerv1.WatchJobsResponse
		select {
		case res := <-recvCh:
			require.NoError(t, res.err)
			resp = res.resp
		case <-time.After(time.Second * 5):
			t.Fatalf("timed out waiting for trigger %d of %d after acknowledging the previous one", i+1, jobs)
		}
		assert.False(t, seen[resp.GetName()], "trigger %s delivered twice", resp.GetName())
		seen[resp.GetName()] = true

		select {
		case res := <-recvCh:
			require.NoError(t, res.err)
			t.Fatalf("trigger %s dispatched while %s held the per-name limit of 1",
				res.resp.GetName(), resp.GetName())
		default:
		}

		require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
				Result: &schedulerv1.WatchJobsRequestResult{
					Id:     resp.GetId(),
					Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
	}

	assert.Len(t, seen, jobs)
}
