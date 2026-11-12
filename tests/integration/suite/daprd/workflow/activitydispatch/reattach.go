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

package activitydispatch

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reattach))
}

// reattach asserts that a running pull activity survives its scheduler
// streams dropping. The sidecar keeps the activity, reconnects, and reports
// it as still running, so the scheduler neither delivers it to the idle
// replica nor counts it as waiting, and its result completes the workflow.
type reattach struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	workflow *workflow.Workflow
}

const reattachAppID = "pull-reattach"

func (r *reattach) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullreattach
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	r.sched = scheduler.New(t)
	r.proxy = proxy.New(t, r.sched)
	r.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(r.sched),
		workflow.WithSchedulerAddress(r.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(reattachAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(reattachAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxy, r.workflow),
	}
}

func (r *reattach) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var started [2]atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 2 {
		r.workflow.RegistryN(i).AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("slow").Await(nil)
		})
		r.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	clients := []*client.TaskHubGrpcClient{
		r.workflow.BackendClientN(t, ctx, 0),
		r.workflow.BackendClientN(t, ctx, 1),
	}
	id, err := clients[0].ScheduleNewWorkflow(ctx, "single", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[0].Load()+started[1].Load())
	}, time.Second*20, time.Millisecond*10)

	gauge := func(name string) float64 {
		return r.sched.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_activity_"+name, "app_id:"+reattachAppID)
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, gauge("inflight"), 0)
	}, time.Second*10, time.Millisecond*10)

	// Twice, so the second drop finds the activity already reattached.
	for range 2 {
		r.proxy.DropWatchJobs()

		// Without reattachment the scheduler would deliver the activity again
		// at once, to the replica with the free slot.
		time.Sleep(time.Second * 3)
		assert.Equal(t, int64(1), started[0].Load()+started[1].Load(), "the running activity must not be delivered again")
		assert.InDelta(t, 0.0, gauge("backlog"), 0, "a reattached activity is not waiting")
		assert.InDelta(t, 1.0, gauge("inflight"), 0, "a reattached activity still counts as running")
	}

	close(releaseCh)
	_, err = clients[0].WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), started[0].Load()+started[1].Load())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, gauge("inflight"), 0)
	}, time.Second*10, time.Millisecond*10)
}
