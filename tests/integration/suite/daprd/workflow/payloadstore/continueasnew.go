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
	"sync/atomic"
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
	suite.Register(new(continueasnew))
}

// continueasnew asserts that a large continue-as-new input is offloaded in
// the new generation's persisted ExecutionStarted event.
type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t, workflow.WithDaprdOptions(0, daprd.WithWorkflowPayloadStoreThreshold(t, 512)))

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	canInput := "can-input-marker-" + strings.Repeat("c", 4096)

	// The second generation's input is offloaded before it reaches the
	// workflow code, so generations are told apart host-side instead of
	// via GetInput.
	var continued atomic.Bool
	c.workflow.Registry().AddWorkflowN("can", func(ctx *task.WorkflowContext) (any, error) {
		if continued.CompareAndSwap(false, true) {
			ctx.ContinueAsNew(canInput)
		}
		return nil, nil
	})

	client := c.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "can", api.WithInput("small first input"))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.True(t, continued.Load())

	// Continue-as-new replaced the history; the persisted ExecutionStarted
	// is the new generation's, carrying the offloaded input.
	events := fworkflow.ReadHistoryEvents(t, ctx, c.workflow.DB(), string(id))
	fworkflow.RequireOffloadedPayload(t, fworkflow.FindPayload(t, events, fworkflow.ExecutionStartedPayload), canInput)
	fworkflow.RequireMarkersAbsent(t, ctx, c.workflow.DB(), "can-input-marker-")
}
