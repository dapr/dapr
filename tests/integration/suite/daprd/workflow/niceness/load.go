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

	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// bottleneck stands in for something the app depends on, such as a state
// store or a downstream service: at most slots activities hold it at once,
// each for service. Activities queue for it in arrival order.
type bottleneck struct {
	sem     chan struct{}
	service time.Duration
}

func newBottleneck(slots int, service time.Duration) *bottleneck {
	return &bottleneck{sem: make(chan struct{}, slots), service: service}
}

// activity returns an activity that calls onStart, then uses the bottleneck.
func (b *bottleneck) activity(onStart func()) func(task.ActivityContext) (any, error) {
	return func(ctx task.ActivityContext) (any, error) {
		if onStart != nil {
			onStart()
		}
		select {
		case b.sem <- struct{}{}:
		case <-ctx.Context().Done():
			return nil, ctx.Context().Err()
		}
		time.Sleep(b.service)
		<-b.sem
		return nil, nil
	}
}

// callActivity is a workflow that runs one activity.
func callActivity(name string) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity(name).Await(nil)
	}
}

// generate starts workflows of name at perSecond until ctx is done, skipping
// a start when 64 are already being created.
func generate(ctx context.Context, cl *client.TaskHubGrpcClient, name string, perSecond int) {
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(perSecond))
		defer ticker.Stop()
		inflight := make(chan struct{}, 64)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case inflight <- struct{}{}:
					go func() {
						defer func() { <-inflight }()
						_, _ = cl.ScheduleNewWorkflow(ctx, name, api.WithStartTime(time.Now()))
					}()
				default:
				}
			}
		}
	}()
}

// waitForLimit waits for an adaptive limit on key, returning the latency
// ratio it was created on: both are read from one scrape.
func waitForLimit(t *testing.T, ctx context.Context, sched *scheduler.Scheduler, key string) float64 {
	t.Helper()
	var ratio float64
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		m := sched.Metrics(c, ctx)
		ratio = m.SumWithLabels("dapr_scheduler_concurrency_latency_ratio", "concurrency_key:"+key)
		assert.Positive(c, m.SumWithLabels("dapr_scheduler_concurrency_limit", "concurrency_key:"+key))
	}, 60*time.Second, 50*time.Millisecond, "an adaptive limit is derived for %s", key)
	return ratio
}

type waits struct{ sum, count float64 }

func waitsOf(t *testing.T, ctx context.Context, sched *scheduler.Scheduler, key string) waits {
	t.Helper()
	return waits{
		sum:   watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_wait_latency_sum", key),
		count: watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_wait_latency_count", key),
	}
}

// meanWait is the mean wait in milliseconds between two readings.
func meanWait(before, after waits) float64 {
	if after.count == before.count {
		return 0
	}
	return (after.sum - before.sum) / (after.count - before.count)
}
