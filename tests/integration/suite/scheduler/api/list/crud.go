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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(crud))
}

type crud struct {
	scheduler *scheduler.Scheduler
	actors    *actors.Actors
}

func (c *crud) Setup(t *testing.T) []framework.Option {
	c.scheduler = scheduler.New(t)
	place := placement.New(t)

	c.actors = actors.New(t,
		actors.WithActorTypes("myactortype"),
		actors.WithScheduler(c.scheduler),
		actors.WithPlacement(place),
	)

	return []framework.Option{
		framework.WithProcesses(c.actors),
	}
}

func (c *crud) Run(t *testing.T, ctx context.Context) {
	c.actors.WaitUntilRunning(t, ctx)

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

	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs())

	client := c.scheduler.Client(t, ctx)
	_, err := client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)

	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "myactorid").GetJobs())

	for i := range 10 {
		_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name:     strconv.Itoa(i),
			Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
			Metadata: jobMetadata("default", "test"),
		})
		require.NoError(t, err)
	}
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "test",
		Job:  &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: &schedulerv1pb.JobMetadata{
			Namespace: "not-default",
			AppId:     "test",
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: new(schedulerv1pb.JobTargetMetadata_Job),
			},
		},
	})
	require.NoError(t, err)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Empty(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs())
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: jobMetadata("default", "not-test"),
	})
	require.NoError(t, err)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 11)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "not-test").GetJobs(), 1)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "not-default", "test").GetJobs(), 1)
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "myactortype", "").GetJobs())
	assert.Empty(t, c.scheduler.ListJobActors(t, ctx, "default", "test", "not-myactortype", "").GetJobs())

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "myactorid"),
	})
	require.NoError(t, err)
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

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "not-myactortype", "myactorid"),
	})
	require.NoError(t, err)
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

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	require.NoError(t, err)
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

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test2",
		Job:      &schedulerv1pb.Job{Schedule: ptr.Of("@every 20s")},
		Metadata: actorMetadata("default", "test", "myactortype", "not-myactorid"),
	})
	require.NoError(t, err)
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

	_, err = client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
		Name:     "test",
		Metadata: jobMetadata("default", "test"),
	})
	require.NoError(t, err)
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

	for i := range 6 {
		_, err = client.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{
			Name:     strconv.Itoa(i),
			Metadata: jobMetadata("default", "test"),
		})
		require.NoError(t, err)
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

	_, err = client.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     "test123",
		Job:      &schedulerv1pb.Job{DueTime: ptr.Of(time.Now().Add(time.Second * 2).Format(time.RFC3339))},
		Metadata: jobMetadata("default", c.actors.AppID()),
	})
	require.NoError(t, err)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", "test").GetJobs(), 4)
	assert.Len(t, c.scheduler.ListJobJobs(t, ctx, "default", c.actors.AppID()).GetJobs(), 1)
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.Empty(col, c.scheduler.ListJobJobs(t, ctx, "default", c.actors.AppID()).GetJobs())
	}, time.Second*10, time.Millisecond*10)
}
