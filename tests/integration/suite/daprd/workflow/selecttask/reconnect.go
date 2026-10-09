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

package selecttask

import (
	"context"
	"fmt"
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
	suite.Register(new(reconnect))
}

// reconnect runs a Select to its blocked point on one worker, disconnects
// that worker, and finishes the instance on a fresh worker that has to
// replay the whole history before it sees the winning event.
type reconnect struct {
	workflow *workflow.Workflow
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("reconnect", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", -1)
		timer := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(event, timer)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the event to win, got index %d", winner)
		}
		var v string
		if err := event.Await(&v); err != nil {
			return nil, err
		}
		return v, nil
	}))

	first := r.workflow.ConnectWorker(t, ctx, reg)
	r.workflow.WaitForConnectedWorkers(t, ctx, 1)

	cl := r.workflow.ManagementClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "reconnect")
	require.NoError(t, err)
	fworkflow.WaitForHistoryEvent(t, ctx, cl, id, func(e *protos.HistoryEvent) bool {
		return e.GetTimerCreated() != nil
	})

	first.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	r.workflow.ConnectWorker(t, ctx, reg)
	r.workflow.WaitForConnectedWorkers(t, ctx, 1)

	require.NoError(t, cl.RaiseEvent(ctx, id, "x", api.WithEventPayload("after reconnect")))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"after reconnect"`, meta.GetOutput().GetValue())
}
