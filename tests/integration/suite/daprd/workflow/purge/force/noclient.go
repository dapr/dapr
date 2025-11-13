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

package force

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(noclient))
}

type noclient struct {
	workflow *workflow.Workflow
}

func (n *noclient) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *noclient) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Minute*60).Await(nil))
		return nil, nil
	})

	client := n.workflow.WorkflowClient(t, ctx)
	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client.StartWorker(sctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "purge")
	require.NoError(t, err)

	db := n.workflow.DB().GetConnection(t)
	tableName := n.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	cancel()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, n.workflow.Dapr().GetMetaActorRuntime(t, ctx).ActiveActors)
	}, time.Second*10, time.Millisecond*10)

	require.Error(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(false)))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)
}
