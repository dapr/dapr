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
	suite.Register(new(base))
}

type base struct {
	workflow *workflow.Workflow
}

func (b *base) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *base) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("foo", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, nil
	})

	wf := b.workflow.WorkflowClient(t, ctx)
	wf.StartWorker(ctx, reg)

	id, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID("abc"))
	require.NoError(t, err)

	_, err = wf.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	resp, err := wf.GetInstanceHistory(ctx, id)
	require.NoError(t, err)

	evs := resp.Events
	require.Len(t, evs, 3)

	assert.NotNil(t, evs[0].GetOrchestratorStarted())
	assert.Nil(t, evs[0].GetExecutionStarted())
	assert.NotNil(t, evs[1].GetExecutionStarted())
	assert.Equal(t, "foo", evs[1].GetExecutionStarted().GetName())
	assert.Equal(t, "abc", evs[1].GetExecutionStarted().GetOrchestrationInstance().GetInstanceId())
	assert.Equal(t, b.workflow.Dapr().AppID(), evs[1].GetRouter().GetSourceAppID())
	assert.NotNil(t, evs[2].GetExecutionCompleted())
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", evs[2].GetExecutionCompleted().GetOrchestrationStatus().String())
	assert.Equal(t, b.workflow.Dapr().AppID(), evs[2].GetRouter().GetSourceAppID())
}
