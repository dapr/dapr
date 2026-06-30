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
	suite.Register(new(happypath))
}

// happypath verifies the stateful-history optimization is actually active end to
// end: a multi-turn workflow completes with the correct result AND the worker
// receives most turns as cached-history deltas rather than full histories. A
// regression that disabled the capability would make Deltas() zero.
type happypath struct {
	workflow *workflow.Workflow
}

func (h *happypath) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(h.workflow)}
}

func (h *happypath) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 10

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Accumulate", accumulate(activityCount)))
	require.NoError(t, registry.AddActivityN("AddOne", addOne))

	worker := h.workflow.ConnectWorker(t, ctx, registry)
	h.workflow.WaitForConnectedWorkers(t, ctx, 1)

	id, err := worker.Client.ScheduleNewWorkflow(ctx, "Accumulate")
	require.NoError(t, err)

	meta, err := worker.Client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)

	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())

	hist, err := worker.Client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assertAccumulateHistory(t, hist.GetEvents(), activityCount)

	assert.GreaterOrEqual(t, worker.Observer.FullSends(), 1, "the cold first turn must be a full send")
	assert.Greater(t, worker.Observer.Deltas(), 0, "later turns must be delivered as deltas")
}
