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

package detached

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fields))
}

// fields asserts that the optional scheduling field exposed on
// ScheduleNewWorkflow (scheduledStartTime) flows from the action through
// the applier into the spawned StartEvent verbatim, and that no
// ParentInstance linkage is set on the spawn — the spawned workflow must
// look like a freshly client-scheduled top-level workflow. The spawned
// instance also receives a minted (non-empty) executionId from the runtime
// since the caller does not supply one.
type fields struct {
	workflow *workflow.Workflow
}

func (f *fields) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fields) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const spawnedInstanceID = "spawned-fields"
	scheduledStart := time.Now().Add(2 * time.Second).UTC()

	f.workflow.Registry().AddWorkflowN("Caller", func(ctx *task.WorkflowContext) (any, error) {
		_, err := ctx.ScheduleNewWorkflow("Spawned",
			task.WithDetachedWorkflowInstanceID(spawnedInstanceID),
			task.WithDetachedWorkflowStartTime(scheduledStart),
		)
		return nil, err
	})
	f.workflow.Registry().AddWorkflowN("Spawned", func(ctx *task.WorkflowContext) (any, error) {
		return "ok", nil
	})

	client := f.workflow.BackendClient(t, ctx)
	parentID, err := client.ScheduleNewWorkflow(ctx, "Caller")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, parentID)
	require.NoError(t, err)

	spawnedMeta, err := client.WaitForWorkflowCompletion(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, spawnedMeta.GetRuntimeStatus())

	hist, err := client.GetInstanceHistory(ctx, api.InstanceID(spawnedInstanceID))
	require.NoError(t, err)

	var startEvent *protos.ExecutionStartedEvent
	for _, e := range hist.GetEvents() {
		if es := e.GetExecutionStarted(); es != nil {
			startEvent = es
			break
		}
	}
	require.NotNil(t, startEvent, "spawned instance must have an ExecutionStartedEvent")

	assert.Nil(t, startEvent.GetParentInstance(),
		"detached spawn's StartEvent must carry no ParentInstance — it must look like a freshly client-scheduled top-level workflow")
	assert.Equal(t, spawnedInstanceID, startEvent.GetWorkflowInstance().GetInstanceId())
	assert.NotEmpty(t, startEvent.GetWorkflowInstance().GetExecutionId().GetValue(),
		"runtime must mint a non-empty executionId when the caller does not supply one")
	require.NotNil(t, startEvent.GetScheduledStartTimestamp())
	assert.WithinDuration(t, scheduledStart, startEvent.GetScheduledStartTimestamp().AsTime(), time.Second,
		"ScheduledStartTimestamp must be propagated through to the spawned StartEvent")
}
