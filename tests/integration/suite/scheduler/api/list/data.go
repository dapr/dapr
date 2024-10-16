/*
Copyright 2024 The Dapr Authors
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

package list

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(data))
}

type data struct {
	scheduler *scheduler.Scheduler
}

func (d *data) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *data) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)
	client := d.scheduler.Client(t, ctx)

	data1, err := anypb.New(wrapperspb.String("hello world"))
	require.NoError(t, err)
	data2, err := anypb.New(wrapperspb.String("hello world 2"))
	require.NoError(t, err)

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "test1",
		Job: &schedulerv1pb.Job{
			Repeats:  ptr.Of(uint32(10)),
			Schedule: ptr.Of("@every 20s"),
			DueTime:  ptr.Of("100s"),
			Ttl:      ptr.Of("200s"),
			Data:     data1,
		},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "test2",
		Job: &schedulerv1pb.Job{
			Repeats:  ptr.Of(uint32(20)),
			Schedule: ptr.Of("@every 40s"),
			DueTime:  ptr.Of("200s"),
			Ttl:      ptr.Of("300s"),
			Data:     data2,
		},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: "mytype", Id: "myid",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	resp, err := client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetJobs(), 1)
	namedJob := resp.GetJobs()[0]
	assert.Equal(t, "test1", namedJob.GetName())
	job := namedJob.GetJob()
	assert.Equal(t, uint32(10), job.GetRepeats())
	assert.Equal(t, "@every 20s", job.GetSchedule())
	assert.Equal(t, "100s", job.GetDueTime())
	assert.Equal(t, "200s", job.GetTtl())
	assert.True(t, proto.Equal(data1, job.GetData()))

	resp, err = client.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "default", AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: "mytype", Id: "myid",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetJobs(), 1)
	namedJob = resp.GetJobs()[0]
	assert.Equal(t, "test2", namedJob.GetName())
	job = namedJob.GetJob()
	assert.Equal(t, uint32(20), job.GetRepeats())
	assert.Equal(t, "@every 40s", job.GetSchedule())
	assert.Equal(t, "200s", job.GetDueTime())
	assert.Equal(t, "300s", job.GetTtl())
	assert.True(t, proto.Equal(data2, job.GetData()))
}
