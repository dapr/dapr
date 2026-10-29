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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restart))
}

// restart terminates a running workflow and starts it again under the same
// instance ID while no worker is connected. The fallback host accepts the
// termination but cannot apply it, as applying it is a turn: the instance
// stays running, so the second start fails synchronously with AlreadyExists,
// and an event raised meanwhile is dropped with the terminated run. Once the
// termination is applied, a start under the same ID on the fallback host takes
// the normal reuse path: the new run gets the event raised for it and
// completes once a worker connects, and nothing of the first run is left.
type restart struct {
	workflow *workflow.Workflow
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return input, nil
	}))

	worker := r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())
	mgmt := r.workflow.ManagementClient(t, ctx)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("fallback-restart"), api.WithInput("first"))
	require.NoError(t, err)
	_, err = mgmt.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	jobs := schedulerJobs(t, ctx, r.workflow)

	// The first run's event timeout is a timer reminder.
	const firstTimer = "||timer-0"
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := jobs(c, id)
		if assert.Len(c, keys, 1) {
			assert.True(c, strings.HasSuffix(keys[0], firstTimer), keys[0])
		}
	}, time.Second*10, time.Millisecond*10)

	worker.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	// The daprd turns into a fallback host in place, so the workflow actor
	// type stays hosted; the retry rides out the disconnect callback.
	// Without the fallback marker the termination would wait for the instance
	// until this deadline.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, mgmt.TerminateWorkflow(octx, id))
	}, time.Second*5, time.Millisecond*50)

	meta, err := mgmt.FetchWorkflowMetadata(octx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, meta.GetRuntimeStatus())

	_, err = mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithInput("second"))
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
	require.NoError(t, mgmt.RaiseEvent(octx, id, "go"))

	// A worker applies the termination, with the event in the same turn.
	worker = r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED, meta.GetRuntimeStatus())
	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, string(id), func(n int) bool { return n == 0 })

	worker.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	octx, cancel = context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, serr := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithInput("second"))
		assert.NoError(c, serr)
	}, time.Second*5, time.Millisecond*50)

	meta, err = mgmt.FetchWorkflowMetadata(octx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_PENDING, meta.GetRuntimeStatus())

	require.NoError(t, mgmt.RaiseEvent(octx, id, "go"))

	// The new run's start and event reminders are staged; no timer of the
	// first run is.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		keys := jobs(c, id)
		assert.Len(c, keys, 2)
		for _, key := range keys {
			assert.False(c, strings.HasSuffix(key, firstTimer), key)
		}
	}, time.Second*10, time.Millisecond*10)

	r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())

	meta, err = mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"second"`, meta.GetOutput().GetValue())

	r.workflow.Scheduler().WaitJobKeyCount(t, ctx, string(id), func(n int) bool { return n == 0 })
}
