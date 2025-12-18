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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(remote))
}

type remote struct {
	workflow *workflow.Workflow
}

func (r *remote) Setup(t *testing.T) []framework.Option {
	uuid, err := uuid.NewRandom()
	require.NoError(t, err)

	r.workflow = workflow.New(t,
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithAppID(uuid.String())),
		workflow.WithDaprdOptions(1, daprd.WithAppID(uuid.String())),
		workflow.WithDaprdOptions(2, daprd.WithAppID(uuid.String())),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *remote) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Minute*60).Await(nil))
		return nil, nil
	})

	client1 := r.workflow.WorkflowClientN(t, ctx, 0)
	client2 := r.workflow.WorkflowClientN(t, ctx, 1)
	client3 := r.workflow.WorkflowClientN(t, ctx, 2)

	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	require.NoError(t, client1.StartWorker(sctx, reg))
	require.NoError(t, client2.StartWorker(sctx, reg))
	require.NoError(t, client3.StartWorker(sctx, reg))

	ids := make([]string, 10)

	for i := range ids {
		id, err := client1.ScheduleWorkflow(ctx, "purge")
		require.NoError(t, err)
		ids[i] = id
	}

	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	cancel()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, r.workflow.DaprN(0).GetMetaActorRuntime(t, ctx).ActiveActors)
		assert.Empty(c, r.workflow.DaprN(1).GetMetaActorRuntime(t, ctx).ActiveActors)
		assert.Empty(c, r.workflow.DaprN(2).GetMetaActorRuntime(t, ctx).ActiveActors)
	}, time.Second*10, time.Millisecond*10)

	for _, id := range ids {
		require.NoError(t, client2.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
	}

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)
}
