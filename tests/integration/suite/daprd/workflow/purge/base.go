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

package purge

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

	b.workflow.Registry().AddOrchestratorN("purge", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	b.workflow.Registry().AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := b.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "purge")
	require.NoError(t, err)

	db := b.workflow.DB().GetConnection(t)
	tableName := b.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.GreaterOrEqual(t, count, 5)

	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	require.NoError(t, client.PurgeOrchestrationState(ctx, id))

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)
}
