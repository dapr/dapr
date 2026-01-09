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
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

type Stalled struct {
	CurrentClient *client.TaskHubGrpcClient
	options       options

	runWorkflowReplica string

	workflows *workflow.Workflow
}

func New(t *testing.T, fopts ...Option) *Stalled {
	t.Helper()

	opts := options{
		workflows:  map[string]task.Orchestrator{},
		activities: map[string]task.Activity{},
	}

	for _, opt := range fopts {
		opt(&opts)
	}

	fw := &Stalled{
		options:            opts,
		runWorkflowReplica: opts.initialReplica,
	}

	appID := uuid.New().String()
	wfOpts := []workflow.Option{
		workflow.WithAddOrchestrator(t, "Orchestrator", fw.workflowWrapper),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
	}
	for name, activity := range opts.activities {
		wfOpts = append(wfOpts, workflow.WithAddActivity(t, name, activity))
	}
	fw.workflows = workflow.New(t, wfOpts...)
	return fw
}

func (f *Stalled) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.Run(t, ctx)
}

func (f *Stalled) Cleanup(t *testing.T) {
	t.Helper()
	f.workflows.Cleanup(t)
}

func (f *Stalled) workflowWrapper(ctx *task.OrchestrationContext) (any, error) {
	if wf, ok := f.options.workflows[f.runWorkflowReplica]; !ok {
		return nil, fmt.Errorf("workflow replica %s not found", f.runWorkflowReplica)
	} else {
		return wf(ctx)
	}
}

func (f *Stalled) ScheduleWorkflow(t *testing.T, ctx context.Context) api.InstanceID {
	t.Helper()
	f.workflows.WaitUntilRunning(t, ctx)
	f.CurrentClient = f.workflows.BackendClient(t, ctx)
	id, err := f.CurrentClient.ScheduleNewOrchestration(ctx, "Orchestrator")
	require.NoError(t, err)
	_, err = f.CurrentClient.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	return id
}

func (f *Stalled) RestartAsReplica(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	f.runWorkflowReplica = name
	f.restart(t, ctx)
}

func (f *Stalled) restart(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.Dapr().Restart(t, ctx)
	f.workflows.WaitUntilRunning(t, ctx)
	f.CurrentClient = f.workflows.BackendClient(t, ctx)
}

func (f *Stalled) waitForStatus(t *testing.T, ctx context.Context, id api.InstanceID, status protos.OrchestrationStatus) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := f.CurrentClient.FetchOrchestrationMetadata(ctx, id)
		require.NoError(c, err)
		assert.Equal(c, status.String(), md.RuntimeStatus.String())
	}, 20*time.Second, 10*time.Millisecond)
}

func (f *Stalled) WaitForStalled(t *testing.T, ctx context.Context, id api.InstanceID) *protos.ExecutionStalledEvent {
	t.Helper()
	f.waitForStatus(t, ctx, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	executionStalled := hist.Events[len(hist.Events)-1].GetExecutionStalled()
	require.NotNil(t, executionStalled)
	return executionStalled
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
	}, 20*time.Second, 10*time.Millisecond)
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
