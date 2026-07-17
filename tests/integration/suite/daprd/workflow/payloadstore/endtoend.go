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

package payloadstore

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(endtoend))
}

// endtoend proves the complete offload/dereference round trip through the
// real daprd binary: large payloads are persisted as references, yet the
// workflow and activity code read the full original values (resolved by
// the dereference hook before events reach the SDK), and metadata queries
// return the full output. This is the one case where app code reads the
// offloaded values on purpose; the other cases assert the persistence
// side.
type endtoend struct {
	workflow *workflow.Workflow
}

func (e *endtoend) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *endtoend) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	input := "e2e-input-marker-" + strings.Repeat("i", 4096)
	activityResult := "e2e-activity-marker-" + strings.Repeat("a", 4096)

	e.workflow.Registry().AddWorkflowN("roundtrip", func(ctx *task.WorkflowContext) (any, error) {
		// The input was offloaded at creation; the dereference hook must
		// hand this code the original value.
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		// Ship the full input to the activity and read its full result.
		var result string
		if err := ctx.CallActivity("echoAndExtend", task.WithActivityInput(in)).Await(&result); err != nil {
			return nil, err
		}
		return result, nil
	})
	e.workflow.Registry().AddActivityN("echoAndExtend", func(ctx task.ActivityContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		if in != input {
			return nil, assert.AnError
		}
		return activityResult, nil
	})

	client := e.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "roundtrip", api.WithInput(input))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The workflow code observed the full input (the activity asserted it)
	// and the metadata query returns the full resolved output.
	assert.Equal(t, fworkflow.JSONString(t, activityResult), meta.GetOutput().GetValue())

	// Persistence still carries references only.
	events := fworkflow.ReadHistoryEvents(t, ctx, e.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload), input)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.TaskScheduledPayload), input)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.TaskCompletedPayload), activityResult)
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionCompletedPayload), activityResult)
	fworkflow.RequireMarkersAbsent(t, ctx, e.workflow.DB(), "e2e-input-marker-", "e2e-activity-marker-")
}
