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
	suite.Register(new(disabled))
}

// disabled is the negative test: a worker that opts out via
// WithStatefulHistoryDisabled does not advertise the capability, so the
// sidecar must send the full history on every turn and never a delta. The
// multi-turn workflow still completes correctly with exactly the expected
// history.
type disabled struct {
	workflow *workflow.Workflow
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(d.workflow)}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 10

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("Accumulate", accumulate(activityCount)))
	require.NoError(t, registry.AddActivityN("AddOne", addOne))

	worker := d.workflow.ConnectWorker(t, ctx, registry, client.WithStatefulHistoryDisabled())
	d.workflow.WaitForConnectedWorkers(t, ctx, 1)

	id, err := worker.Client.ScheduleNewWorkflow(ctx, "Accumulate")
	require.NoError(t, err)

	meta, err := worker.Client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())

	assert.Zero(t, worker.Observer.Deltas(), "a disabled worker must never receive a delta")
	assert.Greater(t, worker.Observer.FullSends(), 1, "every turn must be a full-history send")
	assert.Zero(t, worker.Observer.GetInstanceHistoryCalls(), "no cache means no GetInstanceHistory recovery")

	hist, err := worker.Client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	assertAccumulateHistory(t, hist.GetEvents(), activityCount)
}
