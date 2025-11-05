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
	suite.Register(new(escape))
}

type escape struct {
	workflow *workflow.Workflow
}

func (e *escape) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *escape) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	e.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	e.workflow.BackendClient(t, ctx)

	ids := []string{
		"hello_workflow",
		"workflow_",
		"hello_",
		"_hello",
		"hello_workflow_yoyo",
	}

	wf := e.workflow.WorkflowClient(t, ctx)
	for _, id := range ids {
		_, err := wf.ScheduleWorkflow(ctx, "foo", dworkflow.WithInstanceID(id))
		require.NoError(t, err)
	}

	resp, err := wf.ListInstanceIDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, ids, resp.InstanceIds)
	assert.Nil(t, resp.ContinuationToken)
}
