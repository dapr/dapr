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
	suite.Register(new(disconnect))
}

// disconnect tests that concurrency slots are reclaimed when a watcher
// disconnects with in-flight triggers. A new watcher should be able to receive
// triggers that were previously held.
type disconnect struct {
	scheduler *scheduler.Scheduler
}

func (d *disconnect) Setup(t *testing.T) []framework.Option {
	d.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(d.scheduler),
	}
}

func (d *disconnect) Run(t *testing.T, ctx context.Context) {
	d.scheduler.WaitUntilRunning(t, ctx)

	client := d.scheduler.Client(t, ctx)

	// Schedule 2 recurring jobs so they retrigger after disconnect.
	for _, name := range []string{"job-a", "job-b"} {
		_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
			Name: name,
			Job: &schedulerv1.Job{
				DueTime:  new(time.Now().Format(time.RFC3339)),
				Schedule: new("@every 2s"),
			},
			Metadata: &schedulerv1.JobMetadata{
				AppId: "myapp", Namespace: "default",
				Target: &schedulerv1.JobTargetMetadata{
					Type: &schedulerv1.JobTargetMetadata_Actor{
						Actor: &schedulerv1.TargetActorReminder{
							Type: "mytype", Id: name,
						},
					},
				},
			},
		})
		require.NoError(t, err)
	}

	initial := &schedulerv1.WatchJobsRequestInitial{
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
	}

	// First watcher: receive one trigger, then disconnect WITHOUT acking.
	watchCtx, watchCancel := context.WithCancel(ctx)
	stream1, err := client.WatchJobs(watchCtx)
	require.NoError(t, err)
	require.NoError(t, stream1.Send(&schedulerv1.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1.WatchJobsRequest_Initial{Initial: initial},
	}))

	_, err = stream1.Recv()
	require.NoError(t, err)

	// Disconnect without acking - the in-flight slot should be reclaimed.
	watchCancel()

	// New watcher should be able to receive triggers since the slot was freed.
	triggered := d.scheduler.WatchJobsSuccess(t, ctx, initial)

	select {
	case name := <-triggered:
		assert.NotEmpty(t, name)
	case <-time.After(time.Second * 15):
		t.Fatal("timed out waiting for trigger on new watcher after disconnect")
	}
}
