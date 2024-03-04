//go:build unit
// +build unit

/*
Copyright 2023 The Dapr Authors
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

package actors

import (
	"context"
	"testing"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	testAppID         = "wf-app"
	workflowActorType = "dapr.internal.default.wf-app.workflow"
	activityActorType = "dapr.internal.default.wf-app.activity"
)

func TestNoWorkflowState(t *testing.T) {
	actors := getActorRuntime(t)
	state, err := LoadWorkflowState(context.Background(), actors, "wf1", NewActorsBackendConfig(testAppID))
	require.NoError(t, err)
	assert.Empty(t, state)
}

func TestDefaultWorkflowState(t *testing.T) {
	state := NewWorkflowState(NewActorsBackendConfig(testAppID))
	assert.Equal(t, uint64(1), state.Generation)
}

func TestAddingToInbox(t *testing.T) {
	state := NewWorkflowState(NewActorsBackendConfig(testAppID))
	for i := 0; i < 10; i++ {
		state.AddToInbox(&backend.HistoryEvent{})
	}

	req, err := state.GetSaveRequest("wf1")
	require.NoError(t, err)

	assert.Equal(t, "wf1", req.ActorID)
	assert.Equal(t, workflowActorType, req.ActorType)

	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 11, upsertCount) // 10x inbox + metadata
	assert.Equal(t, 0, deleteCount)
}

func TestClearingInbox(t *testing.T) {
	state := NewWorkflowState(NewActorsBackendConfig(testAppID))
	for i := 0; i < 10; i++ {
		// Simulate the loadng of inbox events from storage
		state.Inbox = append(state.Inbox, &backend.HistoryEvent{})
	}
	state.ClearInbox()

	req, err := state.GetSaveRequest("wf1")
	require.NoError(t, err)

	assert.Equal(t, "wf1", req.ActorID)
	assert.Equal(t, workflowActorType, req.ActorType)

	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 1, upsertCount)  // metadata only
	assert.Equal(t, 10, deleteCount) // the 10 inbox messages should get deleted
}

func TestAddingToHistory(t *testing.T) {
	wfstate := NewWorkflowState(NewActorsBackendConfig(testAppID))
	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		err := runtimeState.AddEvent(&backend.HistoryEvent{})
		require.NoError(t, err)
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)

	req, err := wfstate.GetSaveRequest("wf1")
	require.NoError(t, err)

	assert.Equal(t, "wf1", req.ActorID)
	assert.Equal(t, workflowActorType, req.ActorType)

	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 12, upsertCount) // 10x history + metadata + customStatus
	assert.Equal(t, 0, deleteCount)
}

func TestLoadSavedState(t *testing.T) {
	wfstate := NewWorkflowState(NewActorsBackendConfig(testAppID))

	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		err := runtimeState.AddEvent(&backend.HistoryEvent{EventId: int32(i)})
		require.NoError(t, err)
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)
	wfstate.CustomStatus = "my custom status"

	for i := 0; i < 5; i++ {
		wfstate.AddToInbox(&backend.HistoryEvent{EventId: int32(i)})
	}

	req, err := wfstate.GetSaveRequest("wf1")
	require.NoError(t, err)

	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 17, upsertCount) // 10x history, 5x inbox, 1 metadata, 1 customStatus
	assert.Equal(t, 0, deleteCount)

	actors := getActorRuntime(t)

	err = actors.TransactionalStateOperation(context.Background(), req)
	require.NoError(t, err)

	wfstate, err = LoadWorkflowState(context.Background(), actors, "wf1", NewActorsBackendConfig(testAppID))
	require.NoError(t, err)
	require.NotNil(t, wfstate)

	assert.Equal(t, "my custom status", wfstate.CustomStatus)
	assert.Equal(t, uint64(1), wfstate.Generation)
	require.Len(t, wfstate.History, 10)
	for i, e := range wfstate.History {
		assert.Equal(t, int32(i), e.GetEventId())
	}
	require.Len(t, wfstate.Inbox, 5)
	for i, e := range wfstate.Inbox {
		assert.Equal(t, int32(i), e.GetEventId())
	}
}

func TestDecodeEncodedState(t *testing.T) {
	wfstate := NewWorkflowState(NewActorsBackendConfig(testAppID))
	wfstate.AddToInbox(&backend.HistoryEvent{EventId: int32(1)})
	runtimeState := backend.NewOrchestrationRuntimeState(testAppID, nil)
	err := runtimeState.AddEvent(&backend.HistoryEvent{EventId: int32(2)})
	require.NoError(t, err)
	wfstate.ApplyRuntimeStateChanges(runtimeState)
	wfstate.CustomStatus = "test-status"
	encodedState, err := wfstate.EncodeWorkflowState()
	require.NoError(t, err)
	decodedState := NewWorkflowState(NewActorsBackendConfig(testAppID))
	err = decodedState.DecodeWorkflowState(encodedState)
	require.NoError(t, err)
	assert.Equal(t, wfstate.Inbox[0].GetEventId(), decodedState.Inbox[0].GetEventId())
	assert.Equal(t, wfstate.History[0].GetEventId(), decodedState.History[0].GetEventId())
	assert.Equal(t, wfstate.CustomStatus, decodedState.CustomStatus)
}

func TestResetLoadedState(t *testing.T) {
	wfstate := NewWorkflowState(NewActorsBackendConfig(testAppID))

	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		require.NoError(t, runtimeState.AddEvent(&backend.HistoryEvent{}))
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)

	for i := 0; i < 5; i++ {
		wfstate.AddToInbox(&backend.HistoryEvent{})
	}

	req, err := wfstate.GetSaveRequest("wf1")
	require.NoError(t, err)

	actorRuntime := getActorRuntime(t)
	err = actorRuntime.TransactionalStateOperation(context.Background(), req)
	require.NoError(t, err)

	wfstate, err = LoadWorkflowState(context.Background(), actorRuntime, "wf1", NewActorsBackendConfig(testAppID))
	require.NoError(t, err)
	require.NotNil(t, wfstate)

	assert.Equal(t, uint64(1), wfstate.Generation)
	wfstate.Reset()
	assert.Equal(t, uint64(2), wfstate.Generation)

	req, err = wfstate.GetSaveRequest("wf1")
	require.NoError(t, err)

	assert.Len(t, req.Operations, 17) // history x10 + inbox x5 + metadata + customStatus
	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 2, upsertCount)  // metadata + customStatus
	assert.Equal(t, 15, deleteCount) // all history and inbox records are deleted
}

func getActorRuntime(t *testing.T) actors.Actors {
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:         testAppID,
		ActorsService: "placement:placement:5050",
		AppConfig:     config.ApplicationConfig{},
	})
	compStore := compstore.New()
	compStore.AddStateStore("workflowStore", store)
	act, err := actors.NewActors(actors.ActorsOpts{
		CompStore:      compStore,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  actors.NewMockPlacement(testAppID),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})
	require.NoError(t, err)
	return act
}

func countOperations(t *testing.T, req *actors.TransactionalRequest) (upsertCount, deleteCount int) {
	for _, op := range req.Operations {
		if op.Operation == actors.Upsert {
			upsertCount++
		} else if op.Operation == actors.Delete {
			deleteCount++
		} else {
			t.Fatalf("unexpected operation type: %v", op.Operation)
		}
	}
	return upsertCount, deleteCount
}

func fakeStore() state.Store {
	return daprt.NewFakeStateStore()
}
