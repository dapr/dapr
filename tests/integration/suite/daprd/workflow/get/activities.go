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
	suite.Register(new(activities))
}

type activities struct {
	workflow *workflow.Workflow
}

func (a *activities) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activities) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("a").Await(nil))
		require.NoError(t, ctx.CallActivity("b").Await(nil))
		require.NoError(t, ctx.CallActivity("c").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("a", func(ctx dworkflow.ActivityContext) (any, error) { return "1", nil })
	reg.AddActivityN("b", func(ctx dworkflow.ActivityContext) (any, error) { return "2", nil })
	reg.AddActivityN("c", func(ctx dworkflow.ActivityContext) (any, error) { return "3", nil })

	wf := a.workflow.WorkflowClient(t, ctx)
	wf.StartWorker(ctx, reg)

	id, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = wf.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	resp, err := wf.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	evs := resp.Events

	require.Len(t, evs, 12)

	assert.NotNil(t, evs[0].GetOrchestratorStarted())
	assert.NotNil(t, evs[1].GetExecutionStarted())
	assert.Equal(t, "foo", evs[1].GetExecutionStarted().GetName())
	assert.Equal(t, "abc", evs[1].GetExecutionStarted().GetOrchestrationInstance().GetInstanceId())

	assert.NotNil(t, evs[2].GetTaskScheduled())
	assert.Equal(t, "a", evs[2].GetTaskScheduled().GetName())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[2].GetRouter().GetSourceAppID())
	assert.NotNil(t, evs[3].GetOrchestratorStarted())
	assert.NotNil(t, evs[4].GetTaskCompleted())
	assert.Equal(t, `"1"`, evs[4].GetTaskCompleted().GetResult().GetValue())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[4].GetRouter().GetSourceAppID())

	assert.NotNil(t, evs[5].GetTaskScheduled())
	assert.Equal(t, "b", evs[5].GetTaskScheduled().GetName())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[5].GetRouter().GetSourceAppID())
	assert.NotNil(t, evs[6].GetOrchestratorStarted())
	assert.NotNil(t, evs[7].GetTaskCompleted())
	assert.Equal(t, `"2"`, evs[7].GetTaskCompleted().GetResult().GetValue())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[7].GetRouter().GetSourceAppID())

	assert.NotNil(t, evs[8].GetTaskScheduled())
	assert.Equal(t, "c", evs[8].GetTaskScheduled().GetName())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[8].GetRouter().GetSourceAppID())
	assert.NotNil(t, evs[9].GetOrchestratorStarted())
	assert.NotNil(t, evs[10].GetTaskCompleted())
	assert.Equal(t, `"3"`, evs[10].GetTaskCompleted().GetResult().GetValue())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[10].GetRouter().GetSourceAppID())

	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", evs[11].GetExecutionCompleted().GetOrchestrationStatus().String())
	assert.Equal(t, a.workflow.Dapr().AppID(), evs[11].GetRouter().GetSourceAppID())
}
