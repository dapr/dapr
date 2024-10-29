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
	suite.Register(new(actors))
}

type actors struct {
	scheduler *scheduler.Scheduler
}

func (a *actors) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(a.scheduler),
	}
}

func (a *actors) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	job := a.scheduler.JobNowActor("test1", "namespace", "appid", "actortype", "actorid")

	job.Job.Schedule = ptr.Of("@every 2s")
	_, err := a.scheduler.Client(t, ctx).ScheduleJob(ctx, job)
	require.NoError(t, err)

	wctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	triggered := a.scheduler.WatchJobsSuccess(t, wctx, &schedulerv1pb.WatchJobsRequestInitial{
		Namespace: "namespace", AppId: "appid", ActorTypes: []string{"actortype"},
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		if assert.ElementsMatch(c, []string{"test1"}, triggered.Slice()) {
			cancel()
		}
	}, time.Second*10, time.Millisecond*10)
	time.Sleep(time.Second * 3)
	assert.ElementsMatch(t, []string{"test1"}, triggered.Slice())

	triggered2 := a.scheduler.WatchJobsSuccess(t, ctx, &schedulerv1pb.WatchJobsRequestInitial{
		Namespace: "namespace", AppId: "appid", ActorTypes: []string{"actortype"},
	})
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"test1"}, triggered2.Slice())
	}, time.Second*10, time.Millisecond*10)
	assert.ElementsMatch(t, []string{"test1"}, triggered.Slice())
}
