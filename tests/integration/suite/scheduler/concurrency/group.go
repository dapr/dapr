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
)

func init() {
	suite.Register(new(group))
}

// group tests that a group-level concurrency limit throttles triggers at the
// scheduler level. When the limit is reached, additional triggers are held
// until in-flight jobs are acknowledged.
type group struct {
	scheduler *scheduler.Scheduler
}

func (g *group) Setup(t *testing.T) []framework.Option {
	g.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(g.scheduler),
	}
}

func (g *group) Run(t *testing.T, ctx context.Context) {
	g.scheduler.WaitUntilRunning(t, ctx)

	client := g.scheduler.Client(t, ctx)

	// Schedule 4 jobs in the same group.
	for i := range 4 {
		_, err := client.ScheduleJob(ctx, g.scheduler.JobNowActor(
			"job-"+string(rune('a'+i)), "default", "myapp", "mytype", "id-"+string(rune('a'+i)),
		))
		require.NoError(t, err)
	}

	// Open stream with group limit of 2.
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
				},
			},
		},
	}))

	// Should receive exactly 2 triggers (the limit).
	resp1, err := stream.Recv()
	require.NoError(t, err)
	resp2, err := stream.Recv()
	require.NoError(t, err)

	// No more triggers should arrive while the first 2 are in-flight.
	recvCtx, recvCancel := context.WithTimeout(ctx, time.Second*3)
	defer recvCancel()
	recvCh := make(chan *schedulerv1.WatchJobsResponse, 1)
	go func() {
		resp, err := stream.Recv()
		if err == nil {
			recvCh <- resp
		}
	}()

	select {
	case <-recvCh:
		t.Fatal("received trigger while at concurrency limit")
	case <-recvCtx.Done():
		// Expected - no trigger received within timeout.
	}

	// Ack the first job, freeing a slot.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     resp1.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	// Should now receive a 3rd trigger.
	select {
	case resp := <-recvCh:
		assert.NotNil(t, resp)
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for trigger after ack")
	}

	// Ack the second job.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     resp2.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))
}
