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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(apps))
}

// apps verifies that when the apps of a namespace slow down together, which
// points at capacity they share, the scheduler derives a namespace limit and
// divides it between the apps by their niceness.
type apps struct {
	scheduler *scheduler.Scheduler
}

func (a *apps) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(a.scheduler),
	}
}

func (a *apps) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	client := a.scheduler.Client(t, ctx)

	const (
		typeA = "dapr.internal.default.orders.workflow"
		typeB = "dapr.internal.default.batch.workflow"
	)

	wa := watchjobs.Watch(t, ctx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:        "orders",
		ActorTypes:   []string{typeA},
		ActorAddress: ptr.Of("10.0.0.1:50002"),
		Niceness:     &schedulerv1.WorkflowNiceness{Default: 0},
	})
	wb := watchjobs.Watch(t, ctx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:        "batch",
		ActorTypes:   []string{typeB},
		ActorAddress: ptr.Of("10.0.0.2:50002"),
		Niceness:     &schedulerv1.WorkflowNiceness{Default: 10},
	})

	// Both apps run on the same 4 workers, 20ms each, standing in for a
	// state store or nodes they share.
	srv := watchjobs.NewServer(ctx, 4, 20*time.Millisecond)
	var measuring atomic.Bool
	var countA, countB atomic.Int64
	count := func(c *atomic.Int64) func(*schedulerv1.WatchJobsResponse) {
		return func(*schedulerv1.WatchJobsResponse) {
			if measuring.Load() {
				c.Add(1)
			}
		}
	}
	srv.Serve(ctx, wa, count(&countA))
	srv.Serve(ctx, wb, count(&countB))

	every := 50 * time.Millisecond
	schedule := func(prefix string, n int) {
		for i := range n {
			watchjobs.ScheduleActor(t, ctx, a.scheduler, client, "a-"+prefix+strconv.Itoa(i), "orders", typeA, "wf", &every)
			watchjobs.ScheduleActor(t, ctx, a.scheduler, client, "b-"+prefix+strconv.Itoa(i), "batch", typeB, "wf", &every)
		}
	}
	limitOf := func(c assert.TestingT, key string) float64 {
		return a.scheduler.Metrics(c, ctx).SumWithLabels("dapr_scheduler_concurrency_limit", "concurrency_key:"+key)
	}

	schedule("base-", 2)
	time.Sleep(3 * time.Second)
	assert.Zero(t, limitOf(t, "dapr.internal.default"))

	schedule("load-", 150)

	// Read the ratio and the limit from one scrape, so the ratio seen is the
	// one the limit was created on.
	var ratio float64
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		m := a.scheduler.Metrics(c, ctx)
		ratio = m.SumWithLabels("dapr_scheduler_concurrency_latency_ratio", "concurrency_key:dapr.internal.default")
		assert.Positive(c, m.SumWithLabels("dapr_scheduler_concurrency_limit", "concurrency_key:dapr.internal.default"))
	}, 30*time.Second, 50*time.Millisecond, "a namespace limit is derived when both apps slow together")
	assert.GreaterOrEqual(t, ratio, 2.0, "the namespace latency ratio crossed the overload threshold")
	assert.GreaterOrEqual(t, watchjobs.Metric(t, ctx, a.scheduler, "dapr_scheduler_concurrency_overload_total", "dapr.internal.default"), 1.0)
	assert.Positive(t, watchjobs.Metric(t, ctx, a.scheduler, "dapr_scheduler_concurrency_uncongested_latency", "dapr.internal.default"))

	measuring.Store(true)
	time.Sleep(3 * time.Second)
	measuring.Store(false)

	total := countA.Load() + countB.Load()
	require.Positive(t, total)
	share := float64(countA.Load()) / float64(total)
	t.Logf("dispatched orders=%d batch=%d share=%.2f namespace limit=%v orders limit=%v batch limit=%v",
		countA.Load(), countB.Load(), share,
		limitOf(t, "dapr.internal.default"), limitOf(t, typeA), limitOf(t, typeB))
	assert.GreaterOrEqual(t, share, 0.75, "orders (niceness 0) against batch (niceness 10) should get about 90 percent")
}
