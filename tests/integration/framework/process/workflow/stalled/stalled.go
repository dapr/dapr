/*
Copyright 2025 The Dapr Authors
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

package stalled

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Stalled struct {
	CurrentClient *client.TaskHubGrpcClient
	options       options

	currentDaprIndex int

	workflows *workflow.Workflow
}

func New(t *testing.T, fopts ...Option) *Stalled {
	t.Helper()

	opts := options{
		newWorkflow: nil,
		oldWorkflow: nil,
		activities:  map[string]task.Activity{},
		totalDaprds: 3, // Most tests only need 3 daprds
	}

	for _, opt := range fopts {
		opt(&opts)
	}
	appID := uuid.New().String()
	wfOpts := []workflow.Option{
		workflow.WithDaprds(1),
	}
	for i := 0; i < opts.totalDaprds; i++ {
		wfOpts = append(wfOpts, workflow.WithDaprdOptions(i, daprd.WithAppID(appID)))
	}
	workflows := workflow.New(t, wfOpts...)
	return &Stalled{
		currentDaprIndex: 0,
		CurrentClient:    nil,
		options:          opts,
		workflows:        workflows,
	}
}

func (f *Stalled) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.Run(t, ctx)
}

func (f *Stalled) Cleanup(t *testing.T) {
	t.Helper()
	f.workflows.Cleanup(t)
}

func (f *Stalled) ScheduleWorkflow(t *testing.T, ctx context.Context) api.InstanceID {
	t.Helper()
	f.workflows.WaitUntilRunning(t, ctx)
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, f.currentDaprIndex)
	f.workflows.RegistryN(0).AddOrchestratorN("Orchestrator", f.options.newWorkflow)
	for name, activity := range f.options.activities {
		f.workflows.RegistryN(0).AddActivityN(name, activity)
	}

	// Schedule orchestration (runs on new worker)
	id, err := f.CurrentClient.ScheduleNewOrchestration(ctx, "Orchestrator")
	require.NoError(t, err)
	_, err = f.CurrentClient.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	return id
}

func (f *Stalled) KillCurrentReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.DaprN(f.currentDaprIndex).Kill(t)
}

func (f *Stalled) RunOldReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	index := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(index).Run(t, ctx)
	f.workflows.DaprN(index).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(index).AddOrchestratorN("Orchestrator", f.options.oldWorkflow)
	for name, activity := range f.options.activities {
		f.workflows.RegistryN(index).AddActivityN(name, activity)
	}
	f.currentDaprIndex = index
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, index)
}

func (f *Stalled) RunNewReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	index := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(index).Run(t, ctx)
	f.workflows.DaprN(index).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(index).AddOrchestratorN("Orchestrator", f.options.newWorkflow)
	for name, activity := range f.options.activities {
		f.workflows.RegistryN(index).AddActivityN(name, activity)
	}
	f.currentDaprIndex = index
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, index)
}

func (f *Stalled) SwitchToNewReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.DaprN(1).Kill(t)

	oldDaprDIndex := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(oldDaprDIndex).Run(t, ctx)
	f.workflows.DaprN(oldDaprDIndex).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(oldDaprDIndex).AddOrchestratorN("Orchestrator", f.options.oldWorkflow)
	for name, activity := range f.options.activities {
		f.workflows.RegistryN(oldDaprDIndex).AddActivityN(name, activity)
	}
}

func (f *Stalled) waitForStatus(t *testing.T, ctx context.Context, id api.InstanceID, status protos.OrchestrationStatus) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := f.CurrentClient.FetchOrchestrationMetadata(ctx, id)
		require.NoError(c, err)
		assert.Equal(c, status.String(), md.RuntimeStatus.String())
	}, 20*time.Second, 50*time.Millisecond)
}

func (f *Stalled) WaitForStalled(t *testing.T, ctx context.Context, id api.InstanceID) {
	t.Helper()
	f.waitForStatus(t, ctx, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, hist.Events[len(hist.Events)-1].GetExecutionStalled())
}

func (f *Stalled) WaitForCompleted(t *testing.T, ctx context.Context, id api.InstanceID) {
	t.Helper()
	f.waitForStatus(t, ctx, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, hist.Events[len(hist.Events)-1].GetExecutionCompleted())
}

func (f *Stalled) WaitForNumberOfOrchestrationStartedEvents(t *testing.T, ctx context.Context, id api.InstanceID, expected int) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
		require.NoError(c, err)
		count := 0
		for _, event := range hist.Events {
			if event.GetOrchestratorStarted() != nil {
				count++
			}
		}
		require.Equal(c, expected, count)
	}, 20*time.Second, 50*time.Millisecond)
}

func (f *Stalled) CountStalledEvents(t *testing.T, ctx context.Context, id api.InstanceID) int {
	t.Helper()
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	count := 0
	for _, event := range hist.Events {
		if event.GetExecutionStalled() != nil {
			count++
		}
	}
	return count
}
