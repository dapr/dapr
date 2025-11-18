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

package list

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(purge))
}

type purge struct {
	workflow *workflow.Workflow
}

func (p *purge) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *purge) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	p.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	client := p.workflow.BackendClient(t, ctx)

	ids := make([]string, 0, 5)
	for range 5 {
		id, err := client.ScheduleNewOrchestration(ctx, "foo")
		require.NoError(t, err)
		ids = append(ids, id.String())
	}

	wf := p.workflow.WorkflowClient(t, ctx)
	for range 5 {
		resp, err := wf.ListInstanceIDs(ctx)
		require.NoError(t, err)
		assert.Equal(t, ids, resp.InstanceIds)
		assert.Nil(t, resp.ContinuationToken)
		require.NoError(t, wf.PurgeWorkflowState(ctx, ids[0]))
		ids = ids[1:]
	}

	resp, err := wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, resp.InstanceIds)
	assert.Nil(t, resp.ContinuationToken)

	ids = make([]string, 0, 5)
	for range 5 {
		var id api.InstanceID
		id, err = client.ScheduleNewOrchestration(ctx, "foo")
		require.NoError(t, err)
		ids = append(ids, id.String())
	}

	for range 5 {
		resp, err = wf.ListInstanceIDs(ctx)
		require.NoError(t, err)
		assert.Equal(t, ids, resp.InstanceIds)
		assert.Nil(t, resp.ContinuationToken)
		require.NoError(t, wf.PurgeWorkflowState(ctx, ids[0]))
		ids = ids[1:]
	}

	resp, err = wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, resp.InstanceIds)
	assert.Nil(t, resp.ContinuationToken)
}
