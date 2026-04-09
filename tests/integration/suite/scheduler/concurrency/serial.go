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
	suite.Register(new(serial))
}

// serial tests the edge case of a concurrency limit of 1, which enforces
// strict serialization.
type serial struct {
	scheduler *scheduler.Scheduler
}

func (s *serial) Setup(t *testing.T) []framework.Option {
	s.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(s.scheduler),
	}
}

func (s *serial) Run(t *testing.T, ctx context.Context) {
	s.scheduler.WaitUntilRunning(t, ctx)

	client := s.scheduler.Client(t, ctx)

	for _, name := range []string{"job-a", "job-b", "job-c"} {
		_, err := client.ScheduleJob(ctx, s.scheduler.JobNowActor(
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
					{Group: "mytype", MaxConcurrent: 1},
				},
			},
		},
	}))

	// Receive all triggers into a channel asynchronously.
	recvCh := make(chan *schedulerv1.WatchJobsResponse, 10)
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}
			recvCh <- resp
		}
	}()

	// First trigger arrives immediately.
	var resp *schedulerv1.WatchJobsResponse
	select {
	case resp = <-recvCh:
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for first trigger")
	}

	// No second trigger while first is in-flight.
	select {
	case <-recvCh:
		t.Fatal("received concurrent trigger with limit 1")
	case <-time.After(time.Second * 2):
	}

	// Ack first, second should arrive.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id: resp.GetId(), Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	select {
	case resp = <-recvCh:
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for second trigger")
	}

	// No third trigger while second is in-flight.
	select {
	case <-recvCh:
		t.Fatal("received concurrent trigger with limit 1")
	case <-time.After(time.Second * 2):
	}

	// Ack second, third should arrive.
	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id: resp.GetId(), Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
			},
		},
	}))

	select {
	case resp = <-recvCh:
		assert.NotEmpty(t, resp.GetName())
	case <-time.After(time.Second * 10):
		t.Fatal("timed out waiting for third trigger")
	}
}
