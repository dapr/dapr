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

package workflow

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(refused))
}

// refused checks a workflow turn the sidecar refuses for lack of a workflow
// slot is not a failed reminder and runs once the slot frees.
type refused struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	workflow *workflow.Workflow

	overstate atomic.Bool
}

const refusedAppID = "workflow-refused"

func (r *refused) Setup(t *testing.T) []framework.Option {
	r.overstate.Store(true)
	r.sched = scheduler.New(t)
	// The proxy reports two free workflow slots while the sidecar has one, so
	// the scheduler delivers a second workflow turn while the only slot is held.
	overstate := func(slots *schedulerv1pb.PullSlots) {
		if slots != nil && slots.GetActorType() == r.workflow.WorkflowActorType(0) {
			slots.Free = 2
		}
	}
	r.proxy = proxy.New(t, r.sched, proxy.WithWatchJobsRequestFn(func(req *schedulerv1pb.WatchJobsRequest) {
		if !r.overstate.Load() {
			return
		}
		overstate(req.GetPullSlots())
		for _, slots := range req.GetInitial().GetPullSlots() {
			overstate(slots)
		}
	}))
	r.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(r.sched),
		workflow.WithSchedulerAddress(r.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, daprd.WithAppID(refusedAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowrefused
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxy, r.workflow),
	}
}

func (r *refused) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	held := make(chan struct{})
	var heldOnce sync.Once
	release := make(chan struct{})
	done := ctx.Done()
	var ran atomic.Int64
	registry := r.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first workflow turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			// A redelivered workflow turn runs this again.
			heldOnce.Do(func() { close(held) })
			select {
			case <-release:
			case <-done:
			}
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("plain", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying {
			ran.Add(1)
		}
		return nil, nil
	}))

	client := r.workflow.BackendClient(t, ctx)
	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	select {
	case <-held:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold turn not held")
	}

	// The start of plain blocks until its first workflow turn runs.
	errCh := make(chan error, 1)
	go func() {
		_, err := client.ScheduleNewWorkflow(ctx, "plain",
			api.WithInstanceID("plain"))
		errCh <- err
	}()

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, r.sched.Metrics(col, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_refused_total", "app_id:"+refusedAppID), 1.0)
	}, time.Second*10, time.Millisecond*10)
	r.overstate.Store(false)

	fired := func(success string) float64 {
		return r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_reminders_fired_total",
			"actor_type:"+r.workflow.WorkflowActorType(0), "success:"+success)
	}
	assert.InDelta(t, 0.0, fired("false"), 0, "a refused turn ran nothing and is not a failed reminder")
	assert.Equal(t, int64(0), ran.Load())

	close(release)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		require.Fail(t, "plain did not start")
	}
	for _, id := range []api.InstanceID{hold, "plain"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	}
	assert.Equal(t, int64(1), ran.Load(), "the refused turn runs once the slot frees")
	assert.InDelta(t, 0.0, fired("false"), 0)
}
