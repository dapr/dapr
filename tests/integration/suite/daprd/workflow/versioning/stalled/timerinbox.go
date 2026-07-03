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

package stalled

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(timerinbox))
}

// timerinbox stalls a workflow on the wake of a durable timer and asserts
// that the workflow remains fully manageable afterwards: the state-store
// metadata stays consistent with the persisted inbox keys, status queries
// keep working, and the instance can still be purged.
//
// A TimerFired event woken by a timer reminder is appended to the in-memory
// inbox but is by design never persisted as an inbox-NNNNNN key. Any save
// performed while that event is still in the inbox (such as the stall save)
// must therefore not count it in the metadata inboxLength either, or every
// subsequent load of the workflow state will fail on the phantom inbox key,
// leaving the instance permanently unqueryable and unpurgeable.
type timerinbox struct {
	workflow *workflow.Workflow
}

func (d *timerinbox) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *timerinbox) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	d.workflow.Registry().AddVersionedWorkflowN("workflow", "v1", true, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CreateTimer(time.Second * 4).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	client := d.workflow.BackendClient(t, clientCtx)
	id, err := client.ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, err)

	// Wait for the first execution to be persisted so the durable timer
	// exists before the v1 worker goes away.
	wf.WaitForWorkflowStartedEvent(t, ctx, client, id)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotNil(c, wf.GetLastHistoryEventOfType[protos.HistoryEvent_TimerCreated](t, ctx, client, id))
	}, time.Second*20, time.Millisecond*10)

	// Replace the worker with one that only knows v2, so when the durable
	// timer fires the engine stalls the workflow with VERSION_NOT_AVAILABLE.
	d.workflow.ResetRegistry(t)
	cancelClient()
	d.workflow.WaitForNoConnectedWorkers(t, ctx)

	d.workflow.Registry().AddVersionedWorkflowN("workflow", "v2", true, func(ctx *task.WorkflowContext) (any, error) {
		if err := ctx.CreateTimer(time.Second * 4).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})
	clientCtx, cancelClient = context.WithCancel(ctx)
	defer cancelClient()
	client = d.workflow.BackendClient(t, clientCtx)

	// Wait until the stall has been persisted. The stall is deliberately
	// detected through the state store rather than through the workflow API:
	// that the API keeps working for a stalled workflow is exactly what is
	// asserted below.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var found bool
		for _, raw := range d.workflow.DB().ReadStateValues(t, ctx, string(id), "history") {
			var event protos.HistoryEvent
			if proto.Unmarshal(raw, &event) == nil && event.GetExecutionStalled() != nil {
				found = true
				break
			}
		}
		assert.True(c, found, "expected an ExecutionStalled event to be persisted to the state store")
	}, time.Second*40, time.Millisecond*50)

	// The stall save ran while the in-memory inbox still held the TimerFired
	// event that woke the workflow. TimerFired events are never written as
	// inbox-NNNNNN keys, so the metadata must not count them either.
	_, metaRaw := d.workflow.DB().ReadStateValue(t, ctx, string(id), "metadata")
	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(metaRaw, &metadata))
	inboxKeys := len(d.workflow.DB().ReadStateValues(t, ctx, string(id), "inbox"))
	assert.Equal(t, inboxKeys, int(metadata.GetInboxLength()), //nolint:gosec
		"metadata inboxLength must match the number of persisted inbox-* keys")

	// A stalled workflow must still answer status queries.
	md, err := client.FetchWorkflowMetadata(ctx, id)
	if assert.NoError(t, err, "fetching the metadata of a stalled workflow must succeed") {
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED.String(), md.RuntimeStatus.String())
	}

	// A normal purge of a stalled workflow must be rejected because it is
	// stalled, not because its state can no longer be loaded.
	err = client.PurgeWorkflowState(ctx, id)
	if assert.Error(t, err) {
		assert.Contains(t, err.Error(), "stalled")
	}

	// A stalled workflow must still be force-purgeable.
	assert.NoError(t, client.PurgeWorkflowState(ctx, id, api.WithForcePurge(true)),
		"force purging a stalled workflow must succeed")
}
