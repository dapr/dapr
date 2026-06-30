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
	suite.Register(new(reconnect))
}

// reconnect verifies that when a warm worker drops and a fresh, cold worker takes
// over mid-run, the workflow still completes correctly. The new worker's stream is
// cold and the sidecar dropped the old stream's warm state, so the next turn must
// be re-sent as a full history (re-warming) before deltas resume. This is the
// recovery path that keeps the optimization safe across worker restarts.
type reconnect struct {
	workflow *workflow.Workflow
}

func (r *reconnect) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(r.workflow)}
}

func (r *reconnect) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const activityCount = 5

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("WaitAccumulate", waitAccumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	mgmt := r.workflow.ManagementClient(t, ctx)

	worker1 := r.workflow.ConnectWorker(t, ctx, newRegistry())
	r.workflow.WaitForConnectedWorkers(t, ctx, 1)

	id, err := mgmt.ScheduleNewWorkflow(ctx, "WaitAccumulate")
	require.NoError(t, err)

	_, err = mgmt.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, worker1.Observer.FullSendsFor(string(id)), 1)

	worker1.Disconnect(t)
	r.workflow.WaitForNoConnectedWorkers(t, ctx)

	worker2 := r.workflow.ConnectWorker(t, ctx, newRegistry())
	r.workflow.WaitForConnectedWorkers(t, ctx, 1)

	require.NoError(t, mgmt.RaiseEvent(ctx, id, "go"))

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())

	assert.GreaterOrEqual(t, worker2.Observer.FullSendsFor(string(id)), 1,
		"the reconnected cold worker must be re-warmed with a full history")
}
