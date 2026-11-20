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
	"strings"
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
	suite.Register(new(reattach))
}

// reattach checks a running pull workflow turn survives its scheduler streams
// dropping: it is listed in flight on reconnect, not delivered again, and completes.
type reattach struct {
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
	workflow *workflow.Workflow

	// reattached counts the streams opened listing a running pull reminder.
	reattached atomic.Int64
	// delivered counts the deliveries of hold's timer turn.
	delivered atomic.Int64
}

const reattachAppID = "workflow-reattach"

func (r *reattach) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowreattach
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	r.sched = scheduler.New(t)
	r.proxy = proxy.New(t, r.sched,
		proxy.WithWatchJobsRequestFn(func(req *schedulerv1pb.WatchJobsRequest) {
			if len(req.GetInitial().GetInflightReminders()) > 0 {
				r.reattached.Add(1)
			}
		}),
		proxy.WithWatchJobsResponseFn(func(resp *schedulerv1pb.WatchJobsResponse) {
			if resp.GetMetadata().GetTarget().GetActor().GetId() == "hold" && strings.Contains(resp.GetName(), "timer-") {
				r.delivered.Add(1)
			}
		}),
	)
	r.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(r.sched),
		workflow.WithSchedulerAddress(r.proxy.Address()),
		workflow.WithSigning(false),
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(reattachAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(reattachAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxy, r.workflow),
	}
}

func (r *reattach) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var held atomic.Int64
	release := make(chan struct{})
	for i := range 2 {
		require.NoError(t, r.workflow.RegistryN(i).AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so hold blocks in its second.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !wctx.IsReplaying {
				held.Add(1)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
	}

	client := r.workflow.BackendClientN(t, ctx, 0)
	r.workflow.BackendClientN(t, ctx, 1)

	_, err := client.ScheduleNewWorkflow(ctx, "hold",
		api.WithInstanceID("hold"))
	require.NoError(t, err)

	gauge := func(name string) float64 {
		return r.sched.Metrics(t, ctx).SumWithLabels("dapr_scheduler_workflow_"+name, "app_id:"+reattachAppID)
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), held.Load())
		assert.Equal(c, int64(1), r.delivered.Load())
		assert.InDelta(c, 1.0, gauge("inflight"), 0)
	}, time.Second*10, time.Millisecond*10)

	// Twice, so the second drop finds the workflow turn already reattached.
	for range 2 {
		before := r.reattached.Load()
		r.proxy.DropWatchJobs()

		// The scheduler also holds an unlisted workflow turn for its reattach window,
		// so the listing itself is asserted.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Greater(c, r.reattached.Load(), before)
		}, time.Second*10, time.Millisecond*10)

		// Without reattachment the scheduler would deliver the workflow turn again at
		// once, to the replica with the free slot. Nothing signals that it did
		// not, so give it the time.
		time.Sleep(time.Second * 3)
		assert.Equal(t, int64(1), r.delivered.Load(), "the running turn must not be delivered again")
		assert.InDelta(t, 0.0, gauge("backlog"), 0, "a reattached turn is not waiting")
		assert.InDelta(t, 1.0, gauge("inflight"), 0, "a reattached turn still counts as running")
	}

	close(release)
	meta, err := client.WaitForWorkflowCompletion(ctx, "hold")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, int64(1), held.Load())
	assert.Equal(t, int64(1), r.delivered.Load())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, gauge("inflight"), 0)
	}, time.Second*10, time.Millisecond*10)
}
