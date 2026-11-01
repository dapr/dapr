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
	suite.Register(new(forcepurge))
}

type forcepurge struct {
	workflow *workflow.Workflow
}

func (f *forcepurge) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t, workflow.WithHistorySigning(t))

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *forcepurge) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("sign-forcepurge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		if err := ctx.CallActivity("noop").Await(nil); err != nil {
			return nil, err
		}
		return "", nil
	}))
	require.NoError(t, reg.AddActivityN("noop", func(dworkflow.ActivityContext) (any, error) {
		return nil, nil
	}))

	client := f.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-forcepurge")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	db := f.workflow.DB().GetConnection(t)
	tableName := f.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count, "the signed workflow must have persisted rows before the purge")

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)),
		"force purge must load signed history with the signer wired, not reject it as unverifiable")

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count, "force purge must delete every row, including signatures and certificates")
}
