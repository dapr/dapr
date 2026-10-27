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
	suite.Register(new(multipleclients))
}

// multipleclients connects two workers to the SAME sidecar. The sidecar's shared
// work-item queue spreads turns across both streams with no affinity, so a single
// instance's turns can be split between the two workers. Each (stream, worker)
// pair tracks its own warm prefix and cache independently, so the split must stay
// correct and still produce deltas. All workflows must complete correctly and both
// workers must receive work.
type multipleclients struct {
	workflow *workflow.Workflow
}

func (m *multipleclients) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t)
	return []framework.Option{framework.WithProcesses(m.workflow)}
}

func (m *multipleclients) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const (
		activityCount = 8
		workflowCount = 16
	)

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("Accumulate", accumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	workerA := m.workflow.ConnectWorker(t, ctx, newRegistry())
	workerB := m.workflow.ConnectWorker(t, ctx, newRegistry())
	m.workflow.WaitForConnectedWorkers(t, ctx, 2)

	mgmt := m.workflow.ManagementClient(t, ctx)

	ids := make([]api.InstanceID, workflowCount)
	for i := range ids {
		id, err := mgmt.ScheduleNewWorkflow(ctx, "Accumulate")
		require.NoError(t, err)
		ids[i] = id
	}

	for _, id := range ids {
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())
	}

	aWork := workerA.Observer.FullSends() + workerA.Observer.Deltas()
	bWork := workerB.Observer.FullSends() + workerB.Observer.Deltas()

	assert.Positive(t, aWork, "worker A must receive work")
	assert.Positive(t, bWork, "worker B must receive work")
	assert.Positive(t, workerA.Observer.Deltas()+workerB.Observer.Deltas(), "deltas must flow across two workers")
}
