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

package scheduler

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
	suite.Register(new(unknownack))
}

type unknownack struct {
	scheduler *scheduler.Scheduler
}

func (u *unknownack) Setup(t *testing.T) []framework.Option {
	u.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(u.scheduler),
	}
}

func (u *unknownack) Run(t *testing.T, ctx context.Context) {
	u.scheduler.WaitUntilRunning(t, ctx)

	client := u.scheduler.Client(t, ctx)

	stream, err := client.WatchJobs(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{
			Initial: &schedulerv1.WatchJobsRequestInitial{
				AppId:     "myapp",
				Namespace: "default",
				AcceptJobTypes: []schedulerv1.JobTargetType{
					schedulerv1.JobTargetType_JOB_TARGET_TYPE_JOB,
				},
			},
		},
	}))

	type recvResult struct {
		resp *schedulerv1.WatchJobsResponse
		err  error
	}
	recvCh := make(chan recvResult, 5)
	go func() {
		for {
			resp, rerr := stream.Recv()
			recvCh <- recvResult{resp, rerr}
			if rerr != nil {
				return
			}
		}
	}()

	recv := func(msg string) *schedulerv1.WatchJobsResponse {
		t.Helper()
		select {
		case r := <-recvCh:
			require.NoError(t, r.err, "stream died: %s", msg)
			return r.resp
		case <-time.After(time.Second * 10):
			t.Fatal("timed out waiting for delivery: " + msg)
			return nil
		}
	}

	ack := func(id uint64) {
		require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
				Result: &schedulerv1.WatchJobsRequestResult{
					Id:     id,
					Status: schedulerv1.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
	}

	schedule := func(name string) {
		_, serr := client.ScheduleJob(ctx, u.scheduler.JobNowJob(name, "default", "myapp"))
		require.NoError(t, serr)
	}

	schedule("job-1")
	j1 := recv("job-1")
	require.Equal(t, "job-1", j1.GetName())

	ack(999999)

	ack(j1.GetId())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, u.scheduler.EtcdJobs(t, ctx), "job-1 ack was not processed after the fabricated ack")
	}, time.Second*10, time.Millisecond*10)

	schedule("job-2")
	j2 := recv("job-2 after fabricated ack")
	require.Equal(t, "job-2", j2.GetName())

	// Duplicate ack: the second ack for the same id is dropped silently.
	ack(j2.GetId())
	ack(j2.GetId())

	schedule("job-3")
	j3 := recv("job-3 after duplicate ack")
	require.Equal(t, "job-3", j3.GetName())
	ack(j3.GetId())

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, u.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
}
