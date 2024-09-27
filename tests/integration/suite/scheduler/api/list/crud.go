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
	"strconv"
	"testing"
	"time"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(crud))
}

type crud struct {
	scheduler *scheduler.Scheduler
}

func (c *crud) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(c.scheduler),
	}
}

func (c *crud) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)

	// TODO: @joshvanl: error codes on List API.

	jobMetadata := func(ns, id string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			Namespace: ns, AppId: id,
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{
					Job: new(schedulerv1pb.TargetJob),
				},
			},
		}
	}

	actorMetadata := func(ns, id, actorType, actorID string) *schedulerv1pb.JobMetadata {
		return &schedulerv1pb.JobMetadata{
			Namespace: ns, AppId: "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Actor{
					Actor: &schedulerv1pb.TargetActorReminder{
						Type: actorType, Id: actorID,
					},
				},
			},
		}
	}

	assert.False(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetHasMore())
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs())
	assert.False(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetHasMore())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs())

	client := c.scheduler.Client(t, ctx)
	_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)

	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs())

	for i := range 10 {
		_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name:     strconv.Itoa(i),
			Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
			Metadata: jobMetadata("default", "test"),
		})
	}
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test",
		Job:  &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: &schedulerv1.JobMetadata{
			Namespace: "not-default",
			AppId:     "test",
			Target: &schedulerv1.JobTargetMetadata{
				Type: new(schedulerv1.JobTargetMetadata_Job),
			},
		},
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "not-test"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "myactorid"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "not-myactortype", "myactorid"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs(), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 2)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs(), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test2",
		Job:      &schedulerv1.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs(), 2)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 3)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs(), 1)

	_, err = client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
		Name:     "test",
		Metadata: jobMetadata("default", "test"),
	})
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 10)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs(), 2)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 3)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs(), 1)

	for i := 0; i < 6; i++ {
		_, err = client.DeleteJob(ctx, &schedulerv1.DeleteJobRequest{
			Name:     strconv.Itoa(i),
			Metadata: jobMetadata("default", "test"),
		})
	}
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 4)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "not-default", "test", "myactortype", "myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "myactorid").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "not-myactorid").GetJobs(), 2)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "not-myactorid").GetJobs())
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs(), 3)
	assert.Len(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs(), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name:     "test123",
		Job:      &schedulerv1.Job{DueTime: ptr.Of(time.Now().Add(time.Second * 2).Format(time.RFC3339))},
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 5)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Len(col, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 4)
	}, time.Second*10, time.Millisecond*10)
}
