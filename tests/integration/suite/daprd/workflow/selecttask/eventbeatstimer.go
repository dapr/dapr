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
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(eventbeatstimer))
}

// eventbeatstimer races an external event against a long timer through the
// workflow SDK wrapper. The event arrives first, so Select returns its index
// and the payload is returned as the workflow output.
type eventbeatstimer struct {
	workflow *workflow.Workflow
}

func (e *eventbeatstimer) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *eventbeatstimer) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("eventbeatstimer", func(ctx *dworkflow.WorkflowContext) (any, error) {
		approval := ctx.WaitForExternalEvent("approval", -1)
		timeout := ctx.CreateTimer(time.Second * 60)

		winner, err := ctx.Select(approval, timeout)
		if err != nil {
			return nil, err
		}
		if winner != 0 {
			return nil, fmt.Errorf("expected the event to win, got index %d", winner)
		}

		var payload string
		if err := approval.Await(&payload); err != nil {
			return nil, err
		}
		return "approved:" + payload, nil
	}))

	client := e.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "eventbeatstimer")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client.RaiseEvent(ctx, id, "approval", dworkflow.WithEventPayload("yes")))

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.RuntimeStatus.String())
	assert.JSONEq(t, `"approved:yes"`, meta.Output.GetValue())
}
