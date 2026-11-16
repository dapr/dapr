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

package pull

import (
	"context"
	"runtime"
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
	suite.Register(new(restart))
}

// restart asserts a pull trigger still running when the scheduler restarts
// goes back to the sidecar that lists it as running, without taking a slot,
// instead of to another sidecar with free slots.
type restart struct {
	scheduler *scheduler.Scheduler
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on windows which relies on unix process signals")
	}

	r.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	running := r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(1)))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	_, err := r.scheduler.Client(t, ctx).ScheduleJob(ctx, r.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	require.Equal(t, "pull-1", running.Recv().GetName())

	r.scheduler.RestartGraceful(t, ctx)
	r.scheduler.WaitUntilRunning(t, ctx)

	running = r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0), reminder("instance1::0", "pull-1")))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	resp := running.Recv()
	require.Equal(t, "pull-1", resp.GetName())
	assert.True(t, resp.GetPull(), "a reminder claimed as running is still a pull delivery")

	idle := r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(1)))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	idle.NoRecv(time.Second * 2)

	running.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
	idle.NoRecv(0)
}
