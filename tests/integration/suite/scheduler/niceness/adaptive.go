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
	suite.Register(new(adaptive))
}

// adaptive verifies that with no configured limit, the scheduler detects an
// app's sidecars saturating from its acknowledgement latency and creates a
// limit, after which a light, latency sensitive workflow (hi) keeps its
// latency while a flood of batch work (lo) waits behind it.
type adaptive struct {
	scheduler *scheduler.Scheduler
}

func (a *adaptive) Setup(t *testing.T) []framework.Option {
	a.scheduler = scheduler.New(t)
	return []framework.Option{
		framework.WithProcesses(a.scheduler),
	}
}

func (a *adaptive) Run(t *testing.T, ctx context.Context) {
	a.scheduler.WaitUntilRunning(t, ctx)
	client := a.scheduler.Client(t, ctx)

	const actorType = "dapr.internal.default.myapp.workflow"

	w := watchjobs.Watch(t, ctx, client, &schedulerv1.WatchJobsRequestInitial{
		AppId:        "myapp",
		ActorTypes:   []string{actorType},
		ActorAddress: ptr.Of("10.0.0.1:50002"),
		Niceness: &schedulerv1.WorkflowNiceness{
			Default:   10,
			Workflows: map[string]int32{"hi": -10},
		},
	})

	// The app's sidecars run 4 triggers at a time, 20ms each.
	srv := watchjobs.NewServer(ctx, 4, 20*time.Millisecond)
	var measuring atomic.Bool
	var hi, lo atomic.Int64
	srv.Serve(ctx, w, func(resp *schedulerv1.WatchJobsResponse) {
		if !measuring.Load() {
			return
		}
		if resp.GetMetadata().GetConcurrencyKey() == "hi" {
			hi.Add(1)
		} else {
			lo.Add(1)
		}
	})

	every := 50 * time.Millisecond
	schedule := func(name string, n int) {
		for i := range n {
			watchjobs.ScheduleActor(t, ctx, a.scheduler, client, name+"-"+strconv.Itoa(i), "myapp", actorType, name, &every)
		}
	}
	metric := func(c assert.TestingT, name, key string) float64 {
		return watchjobs.Metric(c, ctx, a.scheduler, name, key)
	}

	// Light load sets each name's baseline latency: hi keeps this load
	// throughout, about 40 a second.
	schedule("hi", 2)
	schedule("lo", 2)
	time.Sleep(3 * time.Second)
	assert.Zero(t, metric(t, "dapr_scheduler_concurrency_limit", actorType), "no limit while the app keeps up")

	// A flood of batch work, far more than the sidecars can run.
	for i := range 150 {
		watchjobs.ScheduleActor(t, ctx, a.scheduler, client, "lo-load-"+strconv.Itoa(i), "myapp", actorType, "lo", &every)
	}

	// Read the ratio and the limit from one scrape, so the ratio seen is the
	// one the limit was created on.
	var ratio float64
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		m := a.scheduler.Metrics(c, ctx)
		ratio = m.SumWithLabels("dapr_scheduler_concurrency_latency_ratio", "concurrency_key:"+actorType)
		assert.Positive(c, m.SumWithLabels("dapr_scheduler_concurrency_limit", "concurrency_key:"+actorType))
	}, 30*time.Second, 50*time.Millisecond, "a limit is derived from acknowledgement latency")
	assert.GreaterOrEqual(t, ratio, 2.0, "the latency ratio crossed the overload threshold")
	assert.GreaterOrEqual(t, metric(t, "dapr_scheduler_concurrency_overload_total", actorType), 1.0)
	uncongested := metric(t, "dapr_scheduler_concurrency_uncongested_latency", actorType)
	assert.Greater(t, uncongested, 5.0, "uncongested round trip in ms, about the 20ms service time")
	assert.Less(t, uncongested, 200.0, "uncongested round trip in ms, about the 20ms service time")
	assert.Zero(t, metric(t, "dapr_scheduler_concurrency_limit", "dapr.internal.default"), "a single app does not limit its namespace")

	// Let the backlog dispatched before the limit drain, then measure.
	time.Sleep(2 * time.Second)
	type waits struct{ sum, count float64 }
	waitsOf := func(name string) waits {
		key := actorType + ":" + name
		return waits{
			sum:   metric(t, "dapr_scheduler_concurrency_wait_latency_sum", key),
			count: metric(t, "dapr_scheduler_concurrency_wait_latency_count", key),
		}
	}
	hiBefore, loBefore := waitsOf("hi"), waitsOf("lo")
	measuring.Store(true)
	time.Sleep(3 * time.Second)
	measuring.Store(false)
	hiAfter, loAfter := waitsOf("hi"), waitsOf("lo")

	require.Positive(t, hiAfter.count-hiBefore.count)
	require.Positive(t, loAfter.count-loBefore.count, "batch work still runs in the capacity hi leaves")
	hiWait := (hiAfter.sum - hiBefore.sum) / (hiAfter.count - hiBefore.count)
	loWait := (loAfter.sum - loBefore.sum) / (loAfter.count - loBefore.count)
	t.Logf("dispatched hi=%d lo=%d mean wait hi=%.1fms lo=%.1fms limit=%v",
		hi.Load(), lo.Load(), hiWait, loWait, metric(t, "dapr_scheduler_concurrency_limit", actorType))
	assert.Less(t, hiWait*10, loWait, "hi (niceness -10) waits a fraction of what lo (niceness 10) waits")
}
