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

// refused asserts that a pull delivery the sidecar refuses for lack of a
// slot is not recorded as a failed reminder, and runs once a slot frees. The
// proxy overstates the sidecar's free slots so the scheduler delivers a
// second activity while the only slot is taken.
type refused struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	workflow *workflow.Workflow

	overstate atomic.Bool
}

const refusedAppID = "pull-refused"

func (r *refused) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullrefused
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	r.overstate.Store(true)
	r.sched = scheduler.New(t)
	r.proxy = proxy.New(t, r.sched, proxy.WithWatchJobsRequestFn(func(req *schedulerv1pb.WatchJobsRequest) {
		if !r.overstate.Load() {
			return
		}
		if slots := req.GetPullSlots(); slots != nil {
			slots.Free = 2
		}
		for _, slots := range req.GetInitial().GetPullSlots() {
			slots.Free = 2
		}
	}))
	r.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(r.sched),
		workflow.WithSchedulerAddress(r.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(refusedAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxy, r.workflow),
	}
}

func (r *refused) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var started atomic.Int64
	releaseCh := make(chan struct{})
	r.workflow.Registry().AddWorkflowN("pair", func(ctx *task.WorkflowContext) (any, error) {
		t1 := ctx.CallActivity("slow")
		t2 := ctx.CallActivity("slow")
		if err := t1.Await(nil); err != nil {
			return nil, err
		}
		return nil, t2.Await(nil)
	})
	r.workflow.Registry().AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
		started.Add(1)
		<-releaseCh
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "pair", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started.Load())
		assert.GreaterOrEqual(c, r.sched.Metrics(c, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_refused_total", "app_id:"+refusedAppID), 1.0)
	}, time.Second*20, time.Millisecond*10)
	r.overstate.Store(false)

	fired := func(success string) float64 {
		return r.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_actor_reminders_fired_total",
			"actor_type:"+r.workflow.ActivityActorType(0), "success:"+success)
	}
	assert.InDelta(t, 0.0, fired("false"), 0, "a refused delivery ran nothing and is not a failed reminder")
	assert.Equal(t, int64(1), started.Load())

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(2), started.Load(), "the refused activity runs once the slot frees")
	assert.InDelta(t, 0.0, fired("false"), 0)
	assert.InDelta(t, 2.0, fired("true"), 0)
}
