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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(metrics))
}

// metrics verifies the niceness gauge follows the streams that declare it,
// and that the wait histogram records how long triggers waited for a slot.
type metrics struct {
	scheduler *scheduler.Scheduler
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	m.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(m.scheduler),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.scheduler.WaitUntilRunning(t, ctx)
	client := m.scheduler.Client(t, ctx)

	const (
		app          = "dapr.internal.default.myapp"
		workflowType = app + ".workflow"
		activityType = app + ".activity"
	)

	metric := func(c assert.TestingT, name, key string) float64 {
		return watchjobs.Metric(c, ctx, m.scheduler, name, key)
	}

	streamCtx, closeStream := context.WithCancel(ctx)
	w := watchjobs.Watch(t, streamCtx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:             "myapp",
		ActorTypes:        []string{workflowType, activityType},
		ConcurrencyLimits: []*schedulerv1.ConcurrencyLimit{watchjobs.LimitActor(workflowType, nil, 1)},
		Niceness: &schedulerv1.WorkflowNiceness{
			Default:    5,
			Workflows:  map[string]int32{"a": -3},
			Activities: map[string]int32{"b": 7},
		},
	})

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 5.0, metric(c, "dapr_scheduler_concurrency_niceness", app), 0)
		assert.InDelta(c, -3.0, metric(c, "dapr_scheduler_concurrency_niceness", workflowType+":a"), 0)
		assert.InDelta(c, 7.0, metric(c, "dapr_scheduler_concurrency_niceness", activityType+":b"), 0)
	}, 10*time.Second, 10*time.Millisecond)

	watchjobs.ScheduleActor(t, ctx, m.scheduler, client, "hold", "myapp", workflowType, "", nil)
	hold := w.Recv(t, "hold")
	for _, name := range []string{"x1", "x2", "x3"} {
		watchjobs.ScheduleActor(t, ctx, m.scheduler, client, name, "myapp", workflowType, "x", nil)
	}
	watchjobs.WaitPending(t, ctx, m.scheduler, workflowType, 3)

	const held = 300 * time.Millisecond
	time.Sleep(held)
	w.Ack(t, hold)
	for range 3 {
		w.Ack(t, w.Recv(t, "waiting trigger"))
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 3.0, metric(c, "dapr_scheduler_concurrency_wait_latency_count", workflowType+":x"), 0)
		assert.GreaterOrEqual(c, metric(c, "dapr_scheduler_concurrency_wait_latency_sum", workflowType+":x"), 3*float64(held.Milliseconds()),
			"each trigger waited at least as long as the hold")
	}, 10*time.Second, 10*time.Millisecond)

	// Once no stream declares it, the niceness in effect is the default.
	closeStream()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, metric(c, "dapr_scheduler_concurrency_niceness", app), 0)
		assert.InDelta(c, 0.0, metric(c, "dapr_scheduler_concurrency_niceness", workflowType+":a"), 0)
		assert.InDelta(c, 0.0, metric(c, "dapr_scheduler_concurrency_niceness", activityType+":b"), 0)
	}, 10*time.Second, 10*time.Millisecond)
}
