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
	suite.Register(new(gateheld))
}

// gateheld asserts a pull trigger held for its sidecar across a stream drop
// keeps its concurrency slot: with a limit of 1, a second trigger waits until
// the first resolves by reference on the new stream.
type gateheld struct {
	scheduler *scheduler.Scheduler
}

func (g *gateheld) Setup(t *testing.T) []framework.Option {
	g.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(g.scheduler),
	}
}

func (g *gateheld) Run(t *testing.T, ctx context.Context) {
	g.scheduler.WaitUntilRunning(t, ctx)

	limited := func(inflight ...*schedulerv1.ActorReminderRef) *schedulerv1.WatchJobsRequestInitial {
		initial := sidecar("addr-1", free(2), inflight...)
		initial.ConcurrencyLimits = []*schedulerv1.ConcurrencyLimit{{
			Target:        &schedulerv1.ConcurrencyLimit_Actor{Actor: &schedulerv1.ConcurrencyLimitActor{Type: actorType}},
			MaxConcurrent: 1,
		}}
		return initial
	}

	stream := g.scheduler.WatchJobsStream(t, ctx, limited())
	g.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)
	client := g.scheduler.Client(t, ctx)
	_, err := client.ScheduleJob(ctx, g.scheduler.JobNowPull("first", namespace, appID, actorType, "instance1::0"))
	require.NoError(t, err)
	require.Equal(t, "first", stream.Recv().GetName())

	stream.Close()
	g.scheduler.WaitUntilSidecarsConnected(t, ctx, 0)
	first := reminder("instance1::0", "first")
	stream = g.scheduler.WatchJobsStream(t, ctx, limited(first))
	g.scheduler.WaitUntilSidecarsConnected(t, ctx, 1)

	_, err = client.ScheduleJob(ctx, g.scheduler.JobNowPull("second", namespace, appID, actorType, "instance2::0"))
	require.NoError(t, err)
	stream.NoRecv(time.Second * 2)

	stream.ResultFor(first, schedulerv1.WatchJobsRequestResultStatus_SUCCESS)
	assert.Equal(t, "second", stream.Recv().GetName(), "the first trigger's result must free its concurrency slot")
}
