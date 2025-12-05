package stalled

import (
	"context"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
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

type StalledFramework struct {
	CurrentClient *client.TaskHubGrpcClient

	appID            string
	currentDaprIndex int
	oldWorkflow      task.Orchestrator
	newWorkflow      task.Orchestrator

	activities map[string]task.Activity

	workflows *workflow.Workflow
}

func NewStalledFramework() *StalledFramework {
	return &StalledFramework{
		appID:            uuid.New().String(),
		currentDaprIndex: 0,
		CurrentClient:    nil,
		activities:       map[string]task.Activity{},
	}
}

func (f *StalledFramework) Setup(t *testing.T) []framework.Option {
	t.Helper()
	f.workflows = workflow.New(t,
		workflow.WithDaprds(1),
		workflow.WithDaprdOptions(0, daprd.WithAppID(f.appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(f.appID)),
		workflow.WithDaprdOptions(2, daprd.WithAppID(f.appID)),
	)

	return []framework.Option{framework.WithProcesses(f.workflows)}
}

func (f *StalledFramework) SetNewWorkflow(t *testing.T, ctx context.Context, orchestrator task.Orchestrator) {
	f.newWorkflow = orchestrator
}
func (f *StalledFramework) SetOldWorkflow(t *testing.T, ctx context.Context, orchestrator task.Orchestrator) {
	f.oldWorkflow = orchestrator
}
func (f *StalledFramework) AddActivityN(t *testing.T, ctx context.Context, name string, activity task.Activity) {
	f.activities[name] = activity
}

func (f *StalledFramework) ScheduleWorkflow(t *testing.T, ctx context.Context) api.InstanceID {
	t.Helper()
	f.workflows.WaitUntilRunning(t, ctx)
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, f.currentDaprIndex)
	f.workflows.RegistryN(0).AddOrchestratorN("Orchestrator", f.newWorkflow)
	for name, activity := range f.activities {
		f.workflows.RegistryN(0).AddActivityN(name, activity)
	}

	// Schedule orchestration (runs on new worker)
	id, err := f.CurrentClient.ScheduleNewOrchestration(ctx, "Orchestrator")
	require.NoError(t, err)
	_, err = f.CurrentClient.WaitForOrchestrationStart(ctx, id)
	require.NoError(t, err)
	return id
}

func (f *StalledFramework) KillCurrentReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.DaprN(f.currentDaprIndex).Kill(t)
}

func (f *StalledFramework) RunOldReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	index := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(index).Run(t, ctx)
	f.workflows.DaprN(index).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(index).AddOrchestratorN("Orchestrator", f.oldWorkflow)
	for name, activity := range f.activities {
		f.workflows.RegistryN(index).AddActivityN(name, activity)
	}
	f.currentDaprIndex = index
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, index)
}

func (f *StalledFramework) RunNewReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	index := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(index).Run(t, ctx)
	f.workflows.DaprN(index).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(index).AddOrchestratorN("Orchestrator", f.newWorkflow)
	for name, activity := range f.activities {
		f.workflows.RegistryN(index).AddActivityN(name, activity)
	}
	f.currentDaprIndex = index
	f.CurrentClient = f.workflows.BackendClientN(t, ctx, index)
}

func (f *StalledFramework) SwitchToNewReplica(t *testing.T, ctx context.Context) {
	t.Helper()
	f.workflows.DaprN(1).Kill(t)

	oldDaprDIndex := f.workflows.RunNewDaprd(t, ctx)
	f.workflows.DaprN(oldDaprDIndex).Run(t, ctx)
	f.workflows.DaprN(oldDaprDIndex).WaitUntilRunning(t, ctx)

	f.workflows.RegistryN(oldDaprDIndex).AddOrchestratorN("Orchestrator", f.oldWorkflow)
	for name, activity := range f.activities {
		f.workflows.RegistryN(oldDaprDIndex).AddActivityN(name, activity)
	}
}

func (f *StalledFramework) waitForStatus(t *testing.T, ctx context.Context, id api.InstanceID, status protos.OrchestrationStatus) {
	t.Helper()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		md, err := f.CurrentClient.FetchOrchestrationMetadata(ctx, id)
		require.NoError(c, err)
		assert.Equal(c, status.String(), md.RuntimeStatus.String())
	}, 20*time.Second, 50*time.Millisecond)
}

func (f *StalledFramework) WaitForStalled(t *testing.T, ctx context.Context, id api.InstanceID) {
	t.Helper()
	f.waitForStatus(t, ctx, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, hist.Events[len(hist.Events)-1].GetExecutionStalled())
}

func (f *StalledFramework) WaitForCompleted(t *testing.T, ctx context.Context, id api.InstanceID) {
	t.Helper()
	f.waitForStatus(t, ctx, id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	hist, err := f.CurrentClient.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, hist.Events[len(hist.Events)-1].GetExecutionCompleted())
}

func (f *StalledFramework) WaitForNumberOfOrchestrationStartedEvents(t *testing.T, ctx context.Context, id api.InstanceID, expected int) {
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
