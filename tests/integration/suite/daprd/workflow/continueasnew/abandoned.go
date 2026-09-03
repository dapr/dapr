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

package continueasnew

import (
	"context"
	"strconv"
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
	suite.Register(new(abandoned))
}

// abandoned verifies that when the engine abandons a work item after
// continuing-as-new (here by exceeding its tight-loop limit), the events the
// previous generation consumed are not re-delivered to the generation that is
// persisted, and that this generation still runs. A leaked event would ride
// the kept-events carryover to the final generation and satisfy its wait
// before the test raises the event again.
type abandoned struct {
	workflow *workflow.Workflow
}

func (a *abandoned) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *abandoned) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	// The engine abandons a work item after 20 tight-loop generations, so this
	// crosses that limit more than once.
	const lastGen = 50
	const id = "can-abandoned"

	reg := a.workflow.Registry()
	reg.AddWorkflowN("can-abandoned", func(ctx *task.WorkflowContext) (any, error) {
		var gen int
		if err := ctx.GetInput(&gen); err != nil {
			return nil, err
		}
		if gen == 0 || gen == lastGen {
			if err := ctx.WaitForSingleEvent("go", time.Hour).Await(nil); err != nil {
				return nil, err
			}
			if gen == lastGen {
				return "done", nil
			}
		}
		ctx.ContinueAsNew(gen+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	client := a.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "can-abandoned",
		api.WithInstanceID(id),
		api.WithInput(0),
	)
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	require.NoError(t, client.RaiseEvent(ctx, id, "go"))

	isLastGenStart := func(e *protos.HistoryEvent) bool {
		return e.GetExecutionStarted().GetInput().GetValue() == strconv.Itoa(lastGen)
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, client, api.InstanceID(id), isLastGenStart))
	}, time.Second*60, time.Millisecond*10)

	meta, err := client.FetchWorkflowMetadata(ctx, api.InstanceID(id))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_RUNNING.String(), meta.GetRuntimeStatus().String(),
		"the event consumed by generation 0 was re-delivered to a later generation")

	require.NoError(t, client.RaiseEvent(ctx, id, "go"))
	waitCtx, cancel := context.WithTimeout(ctx, time.Second*30)
	t.Cleanup(cancel)
	meta, err = client.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err, "the generation persisted by the abandoned work item never ran")
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())
}
