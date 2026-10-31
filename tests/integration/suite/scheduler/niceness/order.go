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

package niceness

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(order))
}

// order verifies that triggers waiting on a saturated gate are served in
// proportion to the weight of their niceness, and in arrival order within a
// name.
type order struct {
	scheduler *scheduler.Scheduler
}

func (n *order) Setup(t *testing.T) []framework.Option {
	n.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(n.scheduler),
	}
}

func (n *order) Run(t *testing.T, ctx context.Context) {
	n.scheduler.WaitUntilRunning(t, ctx)
	client := n.scheduler.Client(t, ctx)

	const actorType = "dapr.internal.default.myapp.workflow"

	w := watchjobs.Watch(t, ctx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:             "myapp",
		ActorTypes:        []string{actorType},
		ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{watchjobs.LimitActor(actorType, nil, 1)},
		Niceness: &schedulerv1.WorkflowNiceness{
			Default:   10,
			Workflows: map[string]int32{"hi": -10},
		},
	})

	watchjobs.ScheduleActor(t, ctx, n.scheduler, client, "hold", "myapp", actorType, "", nil)
	hold := w.Recv(t, "hold")

	for i := range 20 {
		watchjobs.ScheduleActor(t, ctx, n.scheduler, client, "lo-"+strconv.Itoa(i), "myapp", actorType, "lo", nil)
	}
	watchjobs.WaitPending(t, ctx, n.scheduler, actorType, 20)
	for i := range 20 {
		watchjobs.ScheduleActor(t, ctx, n.scheduler, client, "hi-"+strconv.Itoa(i), "myapp", actorType, "hi", nil)
	}
	watchjobs.WaitPending(t, ctx, n.scheduler, actorType, 40)

	w.Ack(t, hold)

	got := make([]string, 0, 40)
	for range 40 {
		resp := w.Recv(t, "pending trigger")
		got = append(got, strings.SplitN(resp.GetName(), "-", 2)[0])
		w.Ack(t, resp)
	}

	// lo started waiting first so takes the first slot; at niceness -10
	// against 10 hi is then served 88 times for every lo, so every hi
	// trigger runs before the second lo.
	want := make([]string, 0, 40)
	want = append(want, "lo")
	for range 20 {
		want = append(want, "hi")
	}
	for range 19 {
		want = append(want, "lo")
	}
	assert.Equal(t, want, got)

	m := func(name, key string) float64 {
		return watchjobs.Metric(t, ctx, n.scheduler, name, key)
	}
	assert.InDelta(t, 10.0, m("dapr_scheduler_concurrency_niceness", "dapr.internal.default.myapp"), 0)
	assert.InDelta(t, -10.0, m("dapr_scheduler_concurrency_niceness", actorType+":hi"), 0)
	assert.InDelta(t, 20.0, m("dapr_scheduler_concurrency_wait_latency_count", actorType+":hi"), 0)
	assert.InDelta(t, 20.0, m("dapr_scheduler_concurrency_wait_latency_count", actorType+":lo"), 0)
	assert.InDelta(t, 41.0, m("dapr_scheduler_concurrency_wait_latency_count", actorType), 0, "every dispatch of the type, including the hold")
}
