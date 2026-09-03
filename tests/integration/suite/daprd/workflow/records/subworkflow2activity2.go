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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(subworkflow2activity2))
}

type subworkflow2activity2 struct {
	workflow *workflow.Workflow
}

func (a *subworkflow2activity2) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *subworkflow2activity2) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddWorkflowN("records", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallChildWorkflow("records2").Await(nil))
		require.NoError(t, ctx.CallChildWorkflow("records2").Await(nil))
		return nil, nil
	})
	a.workflow.Registry().AddWorkflowN("records2", func(ctx *task.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	a.workflow.Registry().AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	db := a.workflow.DB().GetConnection(t)
	tableName := a.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)

	client := a.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "records")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Under WorkflowsFastPath the parent completion can become visible before
	// the last child's own state commit lands, so poll to the steady state.
	expected := 27
	if a.workflow.Signing() {
		// Signing adds 13 rows: parent gets 1 sigcert, 3 signatures and
		// 1 ext-sigcert; each child gets 1 sigcert, 2 signatures and
		// 1 ext-sigcert (activity completion attestation).
		expected = 40
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		if assert.NoError(c, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count)) {
			assert.Equal(c, expected, count)
		}
	}, time.Second*10, time.Millisecond*10)
}
