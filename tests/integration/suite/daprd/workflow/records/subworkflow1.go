/*
Copyright 2025 The Dapr Authors
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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(subworkflow1))
}

type subworkflow1 struct {
	workflow *workflow.Workflow
}

func (a *subworkflow1) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *subworkflow1) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddOrchestratorN("records", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallSubOrchestrator("records2").Await(nil))
		return nil, nil
	})
	a.workflow.Registry().AddOrchestratorN("records2", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	db := a.workflow.DB().GetConnection(t)
	tableName := a.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)

	client := a.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "records")
	require.NoError(t, err)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	// 1. ExecutionStarted – records orchestration begins
	// 2. OrchestratorStarted
	// 3. ExecutionStarted
	// 4. TaskScheduled
	// 5. OrchestratorCompleted
	// 6. ExecutionStarted – records2 orchestration begins
	// 7. OrchestratorStarted
	// 8. OrchestratorCompleted
	// 9. ExecutionCompleted
	// 10. TaskCompleted – result of records2
	// 11. OrchestratorStarted
	// 12. ExecutionCompleted
	// 13. OrchestratorCompleted
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 13, count)
}
