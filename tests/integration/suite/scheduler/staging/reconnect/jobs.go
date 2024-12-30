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

package reconnec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(jobs))
}

type jobs struct {
	scheduler *scheduler.Scheduler
}

func (j *jobs) Setup(t *testing.T) []framework.Option {
	j.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(j.scheduler),
	}
}

func (j *jobs) Run(t *testing.T, ctx context.Context) {
	j.scheduler.WaitUntilRunning(t, ctx)

	job := j.scheduler.JobNowJob("test1", "namespace", "appid")

	job.Job.Schedule = ptr.Of("@every 2s")
	_, err := j.scheduler.Client(t, ctx).ScheduleJob(ctx, job)
	require.NoError(t, err)

	wctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	triggered := j.scheduler.WatchJobsSuccess(t, wctx,
		&schedulerv1pb.WatchJobsRequestInitial{Namespace: "namespace", AppId: "appid"},
	)
	select {
	case name := <-triggered:
		assert.Equal(t, "test1", name)
	case <-time.After(time.Second * 5):
		require.Fail(t, "timed out waiting for job")
	}
	cancel()
	select {
	case <-triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 3):
	}

	triggered2 := j.scheduler.WatchJobsSuccess(t, ctx,
		&schedulerv1pb.WatchJobsRequestInitial{Namespace: "namespace", AppId: "appid"},
	)
	select {
	case name := <-triggered2:
		assert.Equal(t, "test1", name)
	case <-time.After(time.Second * 5):
		require.Fail(t, "timed out waiting for job")
	}
	select {
	case <-triggered:
		assert.Fail(t, "unexpected trigger")
	case <-time.After(time.Second * 2):
	}
}
