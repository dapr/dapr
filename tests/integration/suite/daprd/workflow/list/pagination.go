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
	"github.com/dapr/durabletask-go/task"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(pagination))
}

type pagination struct {
	workflow *workflow.Workflow
}

func (p *pagination) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
		framework.WithIOIntensive(),
	}
}

func (p *pagination) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	p.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	client := p.workflow.BackendClient(t, ctx)

	ids := make([]string, 0, 1030)
	for range 1030 {
		id, err := client.ScheduleNewOrchestration(ctx, "foo")
		require.NoError(t, err)
		ids = append(ids, id.String())
	}

	resp, err := p.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx)
	require.NoError(t, err)
	require.Len(t, resp.InstanceIds, 1024)
	assert.Equal(t, ids[:1024], resp.InstanceIds)
	require.NotNil(t, resp.ContinuationToken)

	resp, err = p.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx, dworkflow.WithListInstanceIDsContinuationToken(*resp.ContinuationToken))
	require.NoError(t, err)
	require.Len(t, resp.InstanceIds, 6)
	assert.Equal(t, ids[1024:], resp.InstanceIds)
	require.Nil(t, resp.ContinuationToken)
}
