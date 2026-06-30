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

package statefulhistory

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(maxinstances))
}

// maxinstances verifies the worker's history cache size cap. With a cap of one,
// caching a second parked instance evicts the first. When the first instance then
// resumes, the sidecar (still warm) sends a delta the worker must recover via
// GetInstanceHistory, and the workflow still completes correctly.
type maxinstances struct {
	workflow *workflow.Workflow
}

func (m *maxinstances) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(m.workflow)}
}

func (m *maxinstances) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 2

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("AccumulateThenWait", accumulateThenWait(activityCount)))
	require.NoError(t, registry.AddActivityN("AddOne", addOne))

	worker := m.workflow.ConnectWorker(t, ctx, registry,
		client.WithWorkflowHistoryCacheMaxInstances(1),
		client.WithWorkflowHistoryCacheTTL(time.Hour),
	)
	m.workflow.WaitForConnectedWorkers(t, ctx, 1)

	mgmt := m.workflow.ManagementClient(t, ctx)

	idA, err := mgmt.ScheduleNewWorkflow(ctx, "AccumulateThenWait")
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, worker.Observer.DeltasFor(string(idA)), 0)
	}, time.Second*30, time.Millisecond*10)
	require.Zero(t, worker.Observer.GetInstanceHistoryCalls())

	idB, err := mgmt.ScheduleNewWorkflow(ctx, "AccumulateThenWait")
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Greater(c, worker.Observer.DeltasFor(string(idB)), 0)
	}, time.Second*30, time.Millisecond*10)

	require.NoError(t, mgmt.RaiseEvent(ctx, idA, "go"))
	metaA, err := mgmt.WaitForWorkflowCompletion(ctx, idA, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metaA))
	assert.Equal(t, strconv.Itoa(activityCount), metaA.GetOutput().GetValue())

	assert.Positive(t, worker.Observer.GetInstanceHistoryCalls(),
		"the evicted instance must recover its history via GetInstanceHistory on resume")

	histA, err := mgmt.GetInstanceHistory(ctx, idA)
	require.NoError(t, err)
	assertAccumulateHistory(t, histA.GetEvents(), activityCount)

	require.NoError(t, mgmt.RaiseEvent(ctx, idB, "go"))
	metaB, err := mgmt.WaitForWorkflowCompletion(ctx, idB, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(metaB))
	assert.Equal(t, strconv.Itoa(activityCount), metaB.GetOutput().GetValue())

	histB, err := mgmt.GetInstanceHistory(ctx, idB)
	require.NoError(t, err)
	assertAccumulateHistory(t, histB.GetEvents(), activityCount)
}
