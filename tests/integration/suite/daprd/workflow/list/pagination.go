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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
	dworkflow "github.com/dapr/durabletask-go/workflow"
	"github.com/dapr/kit/concurrency/slice"
)

func init() {
	suite.Register(new(pagination))
}

type pagination struct {
	workflow *workflow.Workflow
}

func (p *pagination) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithDaprdOptions(0,
			daprd.WithInMemoryActorStateStore("foo"),
		),
	)

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

	const n = 1030

	ids := slice.String()
	errCh := make(chan error, 100)
	for range n {
		go func() {
			id, err := client.ScheduleNewOrchestration(ctx, "foo")
			errCh <- err
			ids.Append(id.String())
		}()
	}

	for range n {
		require.NoError(t, <-errCh)
	}

	resp1, err := p.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx)
	require.NoError(t, err)
	require.Len(t, resp1.InstanceIds, 1024)
	require.NotNil(t, resp1.ContinuationToken)

	resp2, err := p.workflow.WorkflowClient(t, ctx).ListInstanceIDs(ctx, dworkflow.WithListInstanceIDsContinuationToken(*resp1.ContinuationToken))
	require.NoError(t, err)
	require.Len(t, resp2.InstanceIds, 6)
	require.Nil(t, resp2.ContinuationToken)

	assert.ElementsMatch(t, ids.Slice(), append(resp1.InstanceIds, resp2.InstanceIds...))
}
