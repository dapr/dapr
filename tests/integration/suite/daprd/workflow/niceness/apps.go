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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/watchjobs"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(apps))
}

// apps verifies, with real workflows in two apps and no configured limit,
// that when both apps saturate something they share the scheduler derives a
// namespace limit and divides it between the apps by their niceness.
type apps struct {
	workflow *workflow.Workflow
}

const (
	appsOrdersID = "niceness-orders"
	appsBatchID  = "niceness-batch"
)

func (a *apps) Setup(t *testing.T) []framework.Option {
	config := func(name string, niceness string) string {
		return `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: ` + name + `
spec:
  workflow:
    niceness:
      default: ` + niceness + `
`
	}
	a.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, config("nicenessorders", "0")), daprd.WithAppID(appsOrdersID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, config("nicenessbatch", "10")), daprd.WithAppID(appsBatchID)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *apps) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// Both apps' activities share 2 slots, 40ms each: 50 a second.
	shared := newBottleneck(2, 40*time.Millisecond)
	var measuring atomic.Bool
	var orders, batch atomic.Int64
	count := func(n *atomic.Int64) func() {
		return func() {
			if measuring.Load() {
				n.Add(1)
			}
		}
	}
	for i, n := range []*atomic.Int64{&orders, &batch} {
		reg := a.workflow.RegistryN(i)
		reg.AddActivityN("work", shared.activity(count(n)))
		reg.AddWorkflowN("wf", callActivity("work"))
	}

	sched := a.workflow.Scheduler()
	ordersClient := a.workflow.BackendClientN(t, ctx, 0)
	batchClient := a.workflow.BackendClientN(t, ctx, 1)

	loadCtx, stopLoad := context.WithCancel(ctx)
	defer stopLoad()

	generate(loadCtx, ordersClient, "wf", 15)
	generate(loadCtx, batchClient, "wf", 15)
	time.Sleep(4 * time.Second)
	assert.Zero(t, watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", "dapr.internal.default"))

	// Both apps flood the shared dependency.
	generate(loadCtx, ordersClient, "wf", 60)
	generate(loadCtx, batchClient, "wf", 60)

	ratio := waitForLimit(t, ctx, sched, "dapr.internal.default")
	assert.GreaterOrEqual(t, ratio, 2.0, "the namespace latency ratio crossed the overload threshold")

	// Let activities dispatched before the limit drain, then measure.
	time.Sleep(4 * time.Second)
	measuring.Store(true)
	time.Sleep(4 * time.Second)
	measuring.Store(false)
	stopLoad()

	total := orders.Load() + batch.Load()
	require.Positive(t, total)
	share := float64(orders.Load()) / float64(total)
	t.Logf("activities started orders=%d batch=%d share=%.2f namespace limit=%v", orders.Load(), batch.Load(), share,
		watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", "dapr.internal.default"))
	for _, app := range []string{appsOrdersID, appsBatchID} {
		for _, kind := range []string{"workflow", "activity"} {
			key := "dapr.internal.default." + app + "." + kind
			t.Logf("%s limit=%v inflight=%v pending=%v dispatched=%v ratio=%v", key,
				watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_limit", key),
				watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_inflight", key),
				watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_pending", key),
				watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_dispatched_total", key),
				watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_latency_ratio", key))
		}
	}
	t.Logf("namespace inflight=%v pending=%v",
		watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_inflight", "dapr.internal.default"),
		watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_pending", "dapr.internal.default"))
	assert.GreaterOrEqual(t, share, 0.75, "orders (niceness 0) against batch (niceness 10) should get about 90 percent")
}
