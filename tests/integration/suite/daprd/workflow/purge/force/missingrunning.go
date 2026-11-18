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

package force

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/backend"
	dworkflow "github.com/dapr/durabletask-go/workflow"
)

func init() {
	suite.Register(new(missingRunning))
}

// missingRunning force purges a running instance whose metadata declares
// inbox entries that do not exist: its rows and its reminders must go.
type missingRunning struct {
	workflow *workflow.Workflow
}

func (m *missingRunning) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *missingRunning) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	reg := dworkflow.NewRegistry()
	require.NoError(t, reg.AddWorkflowN("missing-running-purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		return nil, ctx.WaitForExternalEvent("go", time.Hour).Await(nil)
	}))

	client := m.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "missing-running-purge")
	require.NoError(t, err)

	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NotEmpty(c, m.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)

	db := m.workflow.DB()

	// Metadata declares two inbox entries that do not exist, as left behind
	// by the inbox length accounting bug fixed in dapr/dapr#10155.
	key, raw := db.ReadStateValue(t, ctx, id, "metadata")
	var metadata backend.BackendWorkflowStateMetadata
	require.NoError(t, proto.Unmarshal(raw, &metadata))
	metadata.InboxLength += 2
	raw, err = proto.Marshal(&metadata)
	require.NoError(t, err)
	db.WriteStateValue(t, ctx, key, raw)

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))

	var count int
	require.NoError(t, db.GetConnection(t).QueryRowContext(ctx, "SELECT COUNT(*) FROM "+db.TableName()).Scan(&count))
	assert.Equal(t, 0, count)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, m.workflow.Scheduler().ListAllKeys(t, ctx, "dapr/jobs"))
	}, time.Second*10, time.Millisecond*10)
}
