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
	"sync"
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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(order))
}

// order verifies that the niceness in an app's Configuration reaches the
// scheduler and orders workflows waiting on a saturated global limit.
type order struct {
	workflow *workflow.Workflow
}

const orderAppID = "niceness-order"

func (o *order) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: nicenessorder
spec:
  workflow:
    globalMaxConcurrentWorkflowInvocations: 1
    niceness:
      default: 10
      workflows:
      - name: hi
        niceness: -10
`
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(orderAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *order) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)
	runOrder(t, ctx, o.workflow, orderAppID)

	sched := o.workflow.Scheduler()
	app := "dapr.internal.default." + orderAppID
	assert.InDelta(t, 10.0, watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_niceness", app), 0)
	assert.InDelta(t, -10.0, watchjobs.Metric(t, ctx, sched, "dapr_scheduler_concurrency_niceness", app+".workflow:hi"), 0)
}

// runOrder holds the app's only slot with a workflow, queues five lo then
// five hi workflows behind it, releases it, and asserts every hi workflow
// runs before the second lo: lo started waiting first so takes the first
// slot, then hi at niceness -10 is served 88 times for every lo at 10.
func runOrder(t *testing.T, ctx context.Context, wf *workflow.Workflow, appID string) {
	t.Helper()

	var holding atomic.Bool
	holdCh := make(chan struct{})
	var lock sync.Mutex
	var started []string

	reg := wf.Registry()
	reg.AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		holding.Store(true)
		<-holdCh
		return nil, nil
	})
	for _, name := range []string{"hi", "lo"} {
		reg.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			lock.Lock()
			started = append(started, name)
			lock.Unlock()
			return nil, nil
		})
	}

	client := wf.BackendClient(t, ctx)
	actorType := "dapr.internal.default." + appID + ".workflow"

	_, err := client.ScheduleNewWorkflow(ctx, "hold", api.WithStartTime(time.Now()))
	require.NoError(t, err)
	assert.Eventually(t, holding.Load, 10*time.Second, 10*time.Millisecond)

	for range 5 {
		_, err = client.ScheduleNewWorkflow(ctx, "lo", api.WithStartTime(time.Now()))
		require.NoError(t, err)
	}
	watchjobs.WaitPending(t, ctx, wf.Scheduler(), actorType, 5)
	for range 5 {
		_, err = client.ScheduleNewWorkflow(ctx, "hi", api.WithStartTime(time.Now()))
		require.NoError(t, err)
	}
	watchjobs.WaitPending(t, ctx, wf.Scheduler(), actorType, 10)

	close(holdCh)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		lock.Lock()
		defer lock.Unlock()
		assert.Len(c, started, 10)
	}, 20*time.Second, 10*time.Millisecond)

	lock.Lock()
	defer lock.Unlock()
	assert.Equal(t, []string{"lo", "hi", "hi", "hi", "hi", "hi", "lo", "lo", "lo", "lo"}, started)
}
