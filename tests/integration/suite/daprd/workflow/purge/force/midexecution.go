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
	"sync/atomic"
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
	suite.Register(new(midexecution))
}

type midexecution struct {
	workflow *workflow.Workflow
}

func (m *midexecution) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *midexecution) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	var inActivity atomic.Bool
	releaseCh := make(chan struct{})

	reg := dworkflow.NewRegistry()
	reg.AddWorkflowN("purge", func(ctx *dworkflow.WorkflowContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc").Await(nil))
		return nil, nil
	})
	reg.AddActivityN("abc", func(ctx dworkflow.ActivityContext) (any, error) {
		inActivity.Store(true)
		<-releaseCh
		return nil, nil
	})

	client := m.workflow.WorkflowClient(t, ctx)
	require.NoError(t, client.StartWorker(ctx, reg))

	id, err := client.ScheduleWorkflow(ctx, "purge")
	require.NoError(t, err)

	db := m.workflow.DB().GetConnection(t)
	tableName := m.workflow.DB().TableName()

	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.GreaterOrEqual(t, count, 5)

	assert.Eventually(t, inActivity.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, client.PurgeWorkflowState(ctx, id, dworkflow.WithForcePurge(true)))
	close(releaseCh)

	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Equal(t, 0, count)
}
