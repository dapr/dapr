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

	// OrchestratorStarted events can appear at varying positions depending on
	// timing, so find each meaningful event by type rather than by index.
	require.GreaterOrEqual(t, len(evs), 6,
		"expected at least 6 events, got %d", len(evs))

	assert.NotNil(t, evs[0].GetWorkflowStarted())

	// Find ExecutionStarted.
	i := 1
	for i < len(evs) && evs[i].GetExecutionStarted() == nil {
		i++
	}
	require.Less(t, i, len(evs), "ExecutionStarted event not found")
	es := evs[i].GetExecutionStarted()
	assert.NotNil(t, es)
	assert.Equal(t, "foo", es.GetName())
	assert.Equal(t, "abc", es.GetWorkflowInstance().GetInstanceId())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[i].GetRouter().GetSourceAppID())

	// Find TaskScheduled.
	i++
	for i < len(evs) && evs[i].GetTaskScheduled() == nil {
		i++
	}
	require.Less(t, i, len(evs), "TaskScheduled event not found")
	ts := evs[i].GetTaskScheduled()
	assert.NotNil(t, ts)
	assert.Equal(t, "a", ts.GetName())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[i].GetRouter().GetSourceAppID())
	assert.Equal(t, c.workflow.DaprN(1).AppID(), evs[i].GetRouter().GetTargetAppID())

	// Find TaskCompleted.
	i++
	for i < len(evs) && evs[i].GetTaskCompleted() == nil {
		i++
	}
	require.Less(t, i, len(evs), "TaskCompleted event not found")
	assert.NotNil(t, evs[i].GetTaskCompleted())
	assert.Equal(t, c.workflow.DaprN(0).AppID(), evs[i].GetRouter().GetSourceAppID())
	assert.Equal(t, c.workflow.DaprN(1).AppID(), evs[i].GetRouter().GetTargetAppID())

	// Find ExecutionCompleted.
	i++
	for i < len(evs) && evs[i].GetExecutionCompleted() == nil {
		i++
	}
	require.Less(t, i, len(evs), "ExecutionCompleted event not found")
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", evs[i].GetExecutionCompleted().GetWorkflowStatus().String())
	assert.Equal(t, c.workflow.Dapr().AppID(), evs[i].GetRouter().GetSourceAppID())
}
