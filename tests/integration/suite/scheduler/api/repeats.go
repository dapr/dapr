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

package api

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
	suite.Register(new(repeats))
}

type repeats struct {
	scheduler *scheduler.Scheduler
}

func (r *repeats) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *repeats) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	client := r.scheduler.Client(t, ctx)

	triggered := r.scheduler.WatchJobsSuccess(t, ctx,
		&schedulerv1.WatchJobsRequestInitial{
			Namespace: "namespace", AppId: "appid",
		},
	)

	metadata := &schedulerv1.JobMetadata{
		Namespace: "namespace", AppId: "appid",
		Target: &schedulerv1.JobTargetMetadata{
			Type: new(schedulerv1.JobTargetMetadata_Job),
		},
	}

	_, err := client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test1", Metadata: metadata,
		Job: &schedulerv1.Job{
			Schedule: ptr.Of("@every 1s"),
			Ttl:      ptr.Of("3s"),
		},
	})
	require.NoError(t, err)

	_, err = client.ScheduleJob(ctx, &schedulerv1.ScheduleJobRequest{
		Name: "test2", Metadata: metadata,
		Job: &schedulerv1.Job{
			Schedule: ptr.Of("@every 1s"),
			Repeats:  ptr.Of(uint32(2)),
		},
	})
	require.NoError(t, err)

	exp := []string{"test1", "test1", "test1", "test2", "test2"}
	got := make([]string, 0, len(exp))
	for range exp {
		select {
		case name := <-triggered:
			got = append(got, name)
		case <-time.After(time.Second * 2):
			require.Fail(t, "timed out waiting for job")
		}
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, got, exp)
	}, time.Second*10, time.Millisecond*10)

	select {
	case <-triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 2):
	}
}
