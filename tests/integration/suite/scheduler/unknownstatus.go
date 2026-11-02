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

	corev1 "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unknownstatus))
}

// unknownstatus asserts that a trigger answered with a result status the
// scheduler does not know is resolved as failed, so its failure policy
// applies, rather than left unresolved when the scheduler closes the stream.
type unknownstatus struct {
	scheduler *scheduler.Scheduler
}

func (u *unknownstatus) Setup(t *testing.T) []framework.Option {
	u.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(u.scheduler),
	}
}

func (u *unknownstatus) Run(t *testing.T, ctx context.Context) {
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

	job := u.scheduler.JobNowJob("job-1", "default", "myapp")
	job.Job.FailurePolicy = &corev1.JobFailurePolicy{
		Policy: &corev1.JobFailurePolicy_Drop{Drop: new(corev1.JobFailurePolicyDrop)},
	}
	_, err = client.ScheduleJob(ctx, job)
	require.NoError(t, err)

	resp, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, "job-1", resp.GetName())

	require.NoError(t, stream.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Result{
			Result: &schedulerv1.WatchJobsRequestResult{
				Id:     resp.GetId(),
				Status: schedulerv1.WatchJobsRequestResultStatus(99),
			},
		},
	}))

	_, err = stream.Recv()
	require.Error(t, err, "the scheduler closes a stream that sends an unknown result status")

	// Resolved as failed, the drop policy deletes the job. Left unresolved,
	// it would stay in flight and in etcd.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, u.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
}
