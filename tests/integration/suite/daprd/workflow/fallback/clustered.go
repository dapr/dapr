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

package fallback

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(clustered))
}

// clustered runs staged operations together with WorkflowsClusteredDeployment,
// where the executor actor type a daprd with a worker registers holds real
// executor actors. The only worker leaves while a workflow waits on an
// activity in progress: the daprd turns into a fallback host in place, which
// halts its executor actors and unregisters the executor actor type, and
// keeps the workflow actor types. It serves the status of the waiting
// workflow and starts another; both complete once a worker connects again,
// and the executor actor type is registered again.
type clustered struct {
	workflow *workflow.Workflow
}

func (c *clustered) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		workflow.WithClusteredDeployment(true),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *clustered) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	// The first run of the activity never returns: the worker running it
	// goes away. Later runs return at once.
	var activityRuns atomic.Int32
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })

	reg := c.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("act", func(ctx task.ActivityContext) (any, error) {
		if activityRuns.Add(1) == 1 {
			select {
			case <-hold:
			case <-ctx.Context().Done():
			}
			return nil, ctx.Context().Err()
		}
		return doneOutput, nil
	}))

	executorType := c.workflow.ExecutorActorType(0)
	workflowType := c.workflow.WorkflowActorType(0)

	worker := c.workflow.ConnectWorker(t, ctx, reg)
	mgmt := c.workflow.ManagementClient(t, ctx)

	// One workflow waits on the held activity; another runs to completion
	// through the executor actors.
	waitingID, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-clustered-waiting"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return activityRuns.Load() == 1
	}, time.Second*10, time.Millisecond*10)
	doneID, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-clustered-done"))
	require.NoError(t, err)
	fworkflow.WaitForCompleted(t, ctx, mgmt, doneID)
	assert.True(t, c.workflow.HostsActorType(t, ctx, 0, executorType))

	worker.Disconnect(t)
	c.workflow.WaitForNoConnectedWorkers(t, ctx)

	// A fallback host: the workflow actor types stay, the executor actor
	// type goes.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.False(col, c.workflow.HostsActorType(col, ctx, 0, executorType))
		assert.True(col, c.workflow.HostsActorType(col, ctx, 0, workflowType))
	}, time.Second*10, time.Millisecond*10)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		meta, merr := mgmt.FetchWorkflowMetadata(octx, waitingID)
		if assert.NoError(col, merr) {
			assert.Equal(col, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())
		}
	}, time.Second*5, time.Millisecond*10)
	afterID, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID("fallback-clustered-after"), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	c.workflow.ConnectWorker(t, ctx, reg)
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.True(col, c.workflow.HostsActorType(col, ctx, 0, executorType))
	}, time.Second*10, time.Millisecond*10)

	for _, id := range []api.InstanceID{waitingID, afterID} {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt, id)
		assert.Equal(t, `"done"`, meta.GetOutput().GetValue(), id)
	}
}
