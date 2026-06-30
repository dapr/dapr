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
	suite.Register(new(baseline))
}

// baseline is a differential test: it runs the identical workflow on a stateful
// worker (deltas) and on a worker with the optimization disabled (full history
// every turn, the pre-optimization behavior), then asserts the two persisted
// histories are equivalent. This proves the optimized delivery reconstructs
// exactly the history the un-optimized path produces, not merely the same output.
//
// The two daprds are separate apps (no shared app ID), so an instance scheduled on
// one runs entirely on that daprd's worker, keeping each run on its chosen worker.
type baseline struct {
	workflow *workflow.Workflow
}

func (b *baseline) Setup(t *testing.T) []framework.Option {
	b.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{framework.WithProcesses(b.workflow)}
}

func (b *baseline) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 8

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("Accumulate", accumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	statefulWorker := b.workflow.ConnectWorkerN(t, ctx, 0, newRegistry())
	baselineWorker := b.workflow.ConnectWorkerN(t, ctx, 1, newRegistry(),
		client.WithStatefulHistoryDisabled())
	b.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	b.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)

	statefulID, err := statefulWorker.Client.ScheduleNewWorkflow(ctx, "Accumulate")
	require.NoError(t, err)
	baselineID, err := baselineWorker.Client.ScheduleNewWorkflow(ctx, "Accumulate")
	require.NoError(t, err)

	statefulMeta, err := statefulWorker.Client.WaitForWorkflowCompletion(ctx, statefulID, api.WithFetchPayloads(true))
	require.NoError(t, err)
	baselineMeta, err := baselineWorker.Client.WaitForWorkflowCompletion(ctx, baselineID, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.WorkflowMetadataIsComplete(statefulMeta))
	assert.True(t, api.WorkflowMetadataIsComplete(baselineMeta))
	assert.Equal(t, strconv.Itoa(activityCount), statefulMeta.GetOutput().GetValue())
	assert.Equal(t, strconv.Itoa(activityCount), baselineMeta.GetOutput().GetValue())

	assert.Greater(t, statefulWorker.Observer.Deltas(), 0, "stateful worker must receive deltas")
	assert.Zero(t, baselineWorker.Observer.Deltas(), "disabled worker must never receive a delta")
	assert.Positive(t, baselineWorker.Observer.FullSends(), "disabled worker receives full histories")

	statefulHist, err := statefulWorker.Client.GetInstanceHistory(ctx, statefulID)
	require.NoError(t, err)
	baselineHist, err := baselineWorker.Client.GetInstanceHistory(ctx, baselineID)
	require.NoError(t, err)

	assert.Equal(t, historySignature(baselineHist.GetEvents()), historySignature(statefulHist.GetEvents()),
		"stateful-history run must reconstruct exactly the same history as the un-optimized baseline")
}
