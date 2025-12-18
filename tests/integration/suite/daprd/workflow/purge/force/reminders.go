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
	suite.Register(new(reminders))
}

type reminders struct {
	workflow *workflow.Workflow
}

func (r *reminders) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reminders) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		require.NoError(t, ctx.CreateTimer(time.Hour).Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := r.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "purge")
	require.NoError(t, err)

	db := r.workflow.DB().GetConnection(t)
	tableName := r.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.GreaterOrEqual(t, count, 5)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"), 1)
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
		assert.Equal(c, 0, count)
		assert.Empty(c, r.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)
}
