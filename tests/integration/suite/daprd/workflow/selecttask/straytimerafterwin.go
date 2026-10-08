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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(straytimerafterwin))
}

// straytimerafterwin lets an event win over a short timer and completes the
// workflow before that timer fires. The late TimerFired must leave the
// completed instance untouched.
type straytimerafterwin struct {
	workflow *workflow.Workflow
}

func (s *straytimerafterwin) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *straytimerafterwin) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	s.workflow.Registry().AddWorkflowN("straytimerafterwin", func(ctx *task.WorkflowContext) (any, error) {
		event := ctx.WaitForSingleEvent("x", -1)
		timer := ctx.CreateTimer(time.Second * 2)

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
	})

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "straytimerafterwin")
	require.NoError(t, err)
	fworkflow.WaitForWorkflowStartedEvent(t, ctx, cl, id)

	require.NoError(t, cl.RaiseEvent(ctx, id, "x", api.WithEventPayload("early")))

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"early"`, meta.GetOutput().GetValue())

	// Outlive the losing timer, then confirm the completed instance did not
	// change and no scheduler job is left behind for it.
	time.Sleep(time.Second * 3)
	meta, err = cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.JSONEq(t, `"early"`, meta.GetOutput().GetValue())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, s.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)
}
