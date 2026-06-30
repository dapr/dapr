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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multipledaprds))
}

// multipledaprds runs many workflows across two daprd sidecars that form a single
// clustered app (shared app ID + WorkflowsClusteredDeployment), each with its own
// connected worker, sharing one placement and state store. Workflow actors are
// distributed across both sidecars, so each worker independently warms and serves
// deltas for the instances placed on its sidecar. All workflows must complete
// correctly, both workers must do work, and deltas must flow.
type multipledaprds struct {
	workflow *workflow.Workflow
}

func (m *multipledaprds) Setup(t *testing.T) []framework.Option {
	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
    name: workflowsclustereddeployment
spec:
    features:
    - name: WorkflowsClusteredDeployment
      enabled: true
`
	appID := uuid.New().String()
	m.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithConfigManifests(t, config)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithConfigManifests(t, config)),
	)
	return []framework.Option{framework.WithProcesses(m.workflow)}
}

func (m *multipledaprds) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	const (
		activityCount = 6
		workflowCount = 12
	)

	newRegistry := func() *task.TaskRegistry {
		reg := task.NewTaskRegistry()
		require.NoError(t, reg.AddWorkflowN("Accumulate", accumulate(activityCount)))
		require.NoError(t, reg.AddActivityN("AddOne", addOne))
		return reg
	}

	worker0 := m.workflow.ConnectWorkerN(t, ctx, 0, newRegistry())
	worker1 := m.workflow.ConnectWorkerN(t, ctx, 1, newRegistry())
	m.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	m.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)

	ids := make([]api.InstanceID, workflowCount)
	for i := range ids {
		id, err := worker0.Client.ScheduleNewWorkflow(ctx, "Accumulate")
		require.NoError(t, err)
		ids[i] = id
	}

	for _, id := range ids {
		meta, err := worker0.Client.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
		assert.Equal(t, strconv.Itoa(activityCount), meta.GetOutput().GetValue())
	}

	work0 := worker0.Observer.FullSends() + worker0.Observer.Deltas()
	work1 := worker1.Observer.FullSends() + worker1.Observer.Deltas()
	totalDeltas := worker0.Observer.Deltas() + worker1.Observer.Deltas()

	assert.Greater(t, work0, 0, "worker on daprd 0 must execute some instances")
	assert.Greater(t, work1, 0, "worker on daprd 1 must execute some instances")
	assert.Greater(t, totalDeltas, 0, "deltas must flow with workflows spread across both sidecars")
}
