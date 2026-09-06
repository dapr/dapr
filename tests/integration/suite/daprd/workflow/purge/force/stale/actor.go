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

package stale

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(actor))
}

// actor verifies a force purge evicts the resident workflow actor, so
// no cached state outlives the purged rows.
type actor struct {
	workflow *workflow.Workflow
}

func (s *actor) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *actor) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, s.workflow.Registry().AddWorkflowN("parked", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))
	client := s.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewWorkflow(ctx, "parked")
	require.NoError(t, err)
	_, err = client.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	wfType := s.workflow.WorkflowActorType(0)
	resident := func(c *assert.CollectT) int {
		for _, a := range s.workflow.Dapr().GetMetaActorRuntime(c, ctx).ActiveActors {
			if a.Type == wfType {
				return a.Count
			}
		}
		return 0
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, resident(c))
	}, time.Second*10, time.Millisecond*10)

	db := s.workflow.DB().GetConnection(t)
	tableName := s.workflow.DB().TableName()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Positive(t, count)

	require.NoError(t, client.PurgeWorkflowState(ctx, id, api.WithForcePurge(true)))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, resident(c), "the purged workflow actor must be evicted")
	}, time.Second*10, time.Millisecond*10)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Zero(t, count)

	require.Error(t, client.RaiseEvent(ctx, id, "go"), "an event against the purged instance must not resurrect it")
	_, err = client.FetchWorkflowMetadata(ctx, id)
	require.ErrorIs(t, err, api.ErrInstanceNotFound)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+tableName).Scan(&count))
	assert.Zero(t, count)
}
