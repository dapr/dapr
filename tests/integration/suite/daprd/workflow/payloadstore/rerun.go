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
	suite.Register(new(rerun))
}

// rerun asserts that a large replacement input supplied on
// RerunWorkflowFromEvent is offloaded in the new instance's persisted
// history.
type rerun struct {
	workflow *workflow.Workflow
}

func (r *rerun) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *rerun) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	rerunInput := "rerun-input-marker-" + strings.Repeat("f", 4096)

	r.workflow.Registry().AddWorkflowN("foo", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	r.workflow.Registry().AddActivityN("bar", func(task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := r.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "foo")
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	newID, err := client.RerunWorkflowFromEvent(ctx, id, 0, api.WithRerunInput(rerunInput))
	require.NoError(t, err)
	meta, err = client.WaitForWorkflowCompletion(ctx, newID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// Rerun from event 0 replaces the input of the first activity task in
	// the new instance's history.
	events := fworkflow.ReadHistoryEvents(t, ctx, r.workflow.DB(), string(newID))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.TaskScheduledPayload), rerunInput)
	fworkflow.RequireMarkersAbsent(t, ctx, r.workflow.DB(), "rerun-input-marker-")
}
