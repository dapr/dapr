/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package get

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(crossactivity))
}

type crossactivity struct {
	workflow *workflow.Workflow
}

func (c *crossactivity) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *crossactivity) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg1 := dworkflow.NewRegistry()
	reg2 := dworkflow.NewRegistry()

	reg1.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("a", dworkflow.WithActivityAppID(c.workflow.DaprN(1).AppID())).Await(nil))
		return nil, nil
	})
	reg2.AddActivityN("a", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	wf1 := c.workflow.WorkflowClientN(t, ctx, 0)
	wf1.StartWorker(ctx, reg1)
	wf2 := c.workflow.WorkflowClientN(t, ctx, 1)
	wf2.StartWorker(ctx, reg2)

	id, err := wf1.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = wf1.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	resp, err := wf1.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	evs := resp.Events

	// Can have 1 or 2 `GetOrchestratorStarted` events depending on timing.
	require.True(t, len(evs) == 7 || len(evs) == 6)

	assert.NotNil(t, evs[0].GetOrchestratorStarted())
	assert.NotNil(t, evs[1].GetExecutionStarted())
	assert.Equal(t, "foo", evs[1].GetExecutionStarted().GetName())
	assert.Equal(t, "abc", evs[1].GetExecutionStarted().GetOrchestrationInstance().GetInstanceId())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[1].GetRouter().GetSourceAppID())

	assert.NotNil(t, evs[2].GetTaskScheduled())
	assert.Equal(t, "a", evs[2].GetTaskScheduled().GetName())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[2].GetRouter().GetSourceAppID())
	assert.Equal(t, c.workflow.DaprN(1).AppID(), evs[2].GetRouter().GetTargetAppID())

	assert.NotNil(t, evs[3].GetOrchestratorStarted())

	// The index of the next events depends on whether there are 6 or 7 events
	// total.
	i := 4
	if len(evs) == 7 {
		i = 5
		assert.NotNil(t, evs[4].GetOrchestratorStarted())
	}

	assert.NotNil(t, evs[i].GetTaskCompleted())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[i].GetRouter().GetSourceAppID())
	assert.Equal(t, c.workflow.DaprN(1).AppID(), evs[i].GetRouter().GetTargetAppID())

	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", evs[i+1].GetExecutionCompleted().GetOrchestrationStatus().String())
	assert.Equal(t, c.workflow.Dapr().AppID(), evs[i+1].GetRouter().GetSourceAppID())
}
