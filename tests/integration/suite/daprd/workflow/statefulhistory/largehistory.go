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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(largehistory))
}

// largehistory drives a workflow through many turns so that the committed history
// grows large, and asserts that deltas dominate (only the cold start is a full
// send) and that the reconstructed history is consistent with a full-history run.
type largehistory struct {
	workflow *workflow.Workflow
}

func (l *largehistory) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(l.workflow)}
}

func (l *largehistory) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 40

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Accumulate", accumulate(activityCount)))
	require.NoError(t, registry.AddActivityN("AddOne", addOne))

	mgmt := l.workflow.ManagementClient(t, ctx)
	worker := l.workflow.ConnectWorker(t, ctx, registry)
	l.workflow.WaitForConnectedWorkers(t, ctx, 1)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "Accumulate")
	require.NoError(t, err)

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())

	deltas := worker.Observer.DeltasFor(string(id))
	fulls := worker.Observer.FullSendsFor(string(id))
	assert.Greater(t, deltas, fulls, "deltas must dominate full sends over a long history")

	hist, err := mgmt.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assertAccumulateHistory(t, hist.GetEvents(), activityCount)
}
