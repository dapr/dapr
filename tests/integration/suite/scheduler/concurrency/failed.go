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

	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(failed))
}

// failed tests that a FAILED ack still releases the concurrency slot, allowing
// pending triggers to proceed.
type failed struct {
	scheduler *scheduler.Scheduler
}

func (f *failed) Setup(t *testing.T) []framework.Option {
	f.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(f.scheduler),
	}
}

func (f *failed) Run(t *testing.T, ctx context.Context) {
	f.scheduler.WaitUntilRunning(t, ctx)

	client := f.scheduler.Client(t, ctx)

	for _, name := range []string{"job-a", "job-b"} {
		_, err := client.ScheduleJob(ctx, f.scheduler.JobNowActor(
			name, "default", "myapp", "mytype", name,
		))
		require.NoError(t, err)
	}

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
						MaxConcurrent: 1,
					},
				},
			},
		},
	}))

	// Receive first trigger (fills the single slot).
	resp1, err := stream.Recv()
	require.NoError(t, err)

	// Second trigger should be held back.
	recvCh := make(chan *schedulerv1.WatchJobsResponse, 1)
	go func() {
		resp, err := stream.Recv()
		if err == nil {
			recvCh <- resp
		}
	}()

	select {
	case <-recvCh:
		t.Fatal("received trigger while at limit")
	case <-time.After(time.Second * 2):
	}

	// Ack the first trigger with FAILED - should still release the slot.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     resp1.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus_FAILED,
			},
		},
	}))

	select {
	case <-recvCh:
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for trigger after FAILED ack")
	}
}
