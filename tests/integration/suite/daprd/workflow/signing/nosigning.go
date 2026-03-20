/*
Copyright 2026 The Dapr Authors
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

package signing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(nosigning))
}

type nosigning struct {
	workflow *workflow.Workflow
}

func (n *nosigning) Setup(t *testing.T) []framework.Option {
	n.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(n.workflow),
	}
}

func (n *nosigning) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("no-sign", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("noop").Await(nil)
	})
	reg.AddActivityN("noop", func(ctx dworkflow.ActivityContext) (any, error) {
		return nil, nil
	})

	client := dworkflow.NewClient(n.workflow.Dapr().GRPCConn(t, ctx))
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "no-sign")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	db := n.workflow.DB().GetConnection(t)
	tableName := n.workflow.DB().TableName()

	var sigCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+tableName+" WHERE key LIKE '%||signature-%'",
	).Scan(&sigCount))
	assert.Equal(t, 0, sigCount, "no signatures should exist without mTLS")

	var certCount int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+tableName+" WHERE key LIKE '%||sigcert-%'",
	).Scan(&certCount))
	assert.Equal(t, 0, certCount, "no signing certificates should exist without mTLS")
}
