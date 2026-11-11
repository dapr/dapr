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
	suite.Register(new(refusedref))
}

// refusedref asserts a held pull trigger that its sidecar refuses by
// reference with NO_CAPACITY, after reattaching, goes to another sidecar with
// a free slot instead of failing.
type refusedref struct {
	scheduler *scheduler.Scheduler
}

func (r *refusedref) Setup(t *testing.T) []framework.Option {
	r.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(r.scheduler),
	}
}

func (r *refusedref) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)

	first := r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(1)))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	_, err := r.scheduler.Client(t, ctx).ScheduleJob(ctx, r.scheduler.JobNowPull("pull-1", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	require.Equal(t, "pull-1", first.Recv().GetName())

	first.Close()
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)
	ref := reminder("instance1::0", "pull-1")
	first = r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-1", free(0), ref))
	other := r.scheduler.WatchJobsStream(t, ctx, sidecar("addr-2", free(1)))
	r.scheduler.WaitUntilSidecarsConnected(t, ctx, 2)
	other.NoRecv(time.Second)

	first.ResultFor(ref, schedulerv1.WatchJobsRequestResultStatus_NO_CAPACITY)
	resp := other.Recv()
	require.Equal(t, "pull-1", resp.GetName())
	assert.True(t, resp.GetPull())
	other.Result(resp.GetId(), schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.scheduler.EtcdJobs(t, ctx))
	}, time.Second*10, time.Millisecond*10)
	first.NoRecv(0)
}
