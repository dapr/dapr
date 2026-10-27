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
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(forcepurgetampered))
}

type forcepurgetampered struct {
	workflow *workflow.Workflow
}

func (f *forcepurgetampered) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t, workflow.WithHistorySigning(t))

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *forcepurgetampered) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("sign-fpt", func(ctx *dworkflow.WorkflowContext) (any, error) {
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

	id, err := client.ScheduleWorkflow(ctx, "sign-fpt")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// Tamper a history event so the signature chain no longer verifies.
	histKey, raw := f.workflow.DB().FirstStateValue(t, ctx, id, "history")
	var evt protos.HistoryEvent
	require.NoError(t, proto.Unmarshal(raw, &evt))
	evt.EventId += 9999
	updated, err := proto.Marshal(&evt)
	require.NoError(t, err)
	f.workflow.DB().WriteStateValue(t, ctx, histKey, updated)

	// Restart to drop the cached (untampered) state.
	f.workflow.Dapr().Restart(t, ctx)
	f.workflow.WaitUntilRunning(t, ctx)

	client = f.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	db := f.workflow.DB().GetConnection(t)
	tableName := f.workflow.DB().TableName()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count, "the tampered workflow must still have rows to purge")

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)),
		"force purge must proceed despite the signature verification failure")

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count, "force purge must delete every row of the tampered workflow")
}
