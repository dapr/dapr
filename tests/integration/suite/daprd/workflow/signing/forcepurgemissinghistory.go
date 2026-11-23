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
	suite.Register(new(forcepurgemissinghistory))
}

// forcepurgemissinghistory force purges a signed instance with a missing
// history entry: the remaining history no longer verifies, and every row,
// including signatures and certificates, must still be deleted.
type forcepurgemissinghistory struct {
	workflow *workflow.Workflow
}

func (m *forcepurgemissinghistory) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t, workflow.WithHistorySigning(t))

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *forcepurgemissinghistory) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("sign-forcepurge-missing-history", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("noop").Await(nil)
	}))
	require.NoError(t, reg.AddActivityN("noop", func(dworkflow.ActivityContext) (any, error) {
		return nil, nil
	}))

	client := m.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "sign-forcepurge-missing-history")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	db := m.workflow.DB()
	require.Greater(t, len(db.ReadStateValues(t, ctx, id, "history")), 1)

	// Metadata still declares the first history entry, but its row is gone.
	db.DeleteStateKeys(t, ctx, "%||"+id+"||history-000000")

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	var count int
	require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+db.TableName()).Scan(&count))
	assert.Equal(t, 0, count, "force purge must delete every row, including signatures and certificates")
}
