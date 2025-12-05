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
	suite.Register(new(childwf))
}

type childwf struct {
	workflow *workflow.Workflow
}

func (c *childwf) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *childwf) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallChildWorkflow("bar").Await(nil))
		return nil, nil
	})
	reg.AddWorkflowN("bar", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	wf := c.workflow.WorkflowClient(t, ctx)
	wf.StartWorker(ctx, reg)

	id, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = wf.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	resp, err := wf.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	evs := resp.Events

	require.Len(t, evs, 6)

	assert.NotNil(t, evs[0].GetOrchestratorStarted())
	assert.Nil(t, evs[0].GetExecutionStarted())
	assert.NotNil(t, evs[1].GetExecutionStarted())
	assert.Equal(t, "foo", evs[1].GetExecutionStarted().GetName())
	assert.Equal(t, "abc", evs[1].GetExecutionStarted().GetOrchestrationInstance().GetInstanceId())
	assert.Equal(t, c.workflow.Dapr().AppID(), evs[1].GetRouter().GetSourceAppID())

	assert.NotNil(t, evs[2].GetSubOrchestrationInstanceCreated())
	assert.Equal(t, "abc:0000", evs[2].GetSubOrchestrationInstanceCreated().GetInstanceId())
	assert.Equal(t, "bar", evs[2].GetSubOrchestrationInstanceCreated().GetName())
	assert.Equal(t, c.workflow.Dapr().AppID(), evs[2].GetRouter().GetSourceAppID())

	assert.NotNil(t, evs[3].GetOrchestratorStarted())

	assert.NotNil(t, evs[4].GetSubOrchestrationInstanceCompleted())

	assert.NotNil(t, evs[5].GetExecutionCompleted())
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", evs[5].GetExecutionCompleted().GetOrchestrationStatus().String())
	assert.Equal(t, c.workflow.Dapr().AppID(), evs[5].GetRouter().GetSourceAppID())
}
