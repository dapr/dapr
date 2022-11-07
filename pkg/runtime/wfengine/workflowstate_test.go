/*
Copyright 2022 The Dapr Authors
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
package wfengine_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

func TestNoWorkflowState(t *testing.T) {
	actors := getActorRuntime()
	state, err := wfengine.LoadWorkflowState(context.Background(), actors, "wf1")
	assert.NoError(t, err)
	assert.Nil(t, state)
}

func TestAddingToInbox(t *testing.T) {
	state := wfengine.NewWorkflowState(uuid.New())
	for i := 0; i < 10; i++ {
		state.AddToInbox(&backend.HistoryEvent{})
	}

	req, err := state.GetSaveRequest("wf1")
	if assert.NoError(t, err) {
		assert.Equal(t, "wf1", req.ActorID)
		assert.Equal(t, wfengine.WorkflowActorType, req.ActorType)

		upsertCount, deleteCount := countOperations(t, req)
		assert.Equal(t, 12, upsertCount) // 10x inbox + metadata + customStatus
		assert.Equal(t, 0, deleteCount)
	}
}

func TestClearingInbox(t *testing.T) {
	state := wfengine.NewWorkflowState(uuid.New())
	for i := 0; i < 10; i++ {
		// Simulate the loadng of inbox events from storage
		state.Inbox = append(state.Inbox, &backend.HistoryEvent{})
	}
	state.ClearInbox()

	req, err := state.GetSaveRequest("wf1")
	if assert.NoError(t, err) {
		assert.Equal(t, "wf1", req.ActorID)
		assert.Equal(t, wfengine.WorkflowActorType, req.ActorType)

		upsertCount, deleteCount := countOperations(t, req)
		assert.Equal(t, 2, upsertCount)  // metadata + customStatus
		assert.Equal(t, 10, deleteCount) // the 10 inbox messages should get deleted
	}
}

func TestAddingToHistory(t *testing.T) {
	wfstate := wfengine.NewWorkflowState(uuid.New())
	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		if err := runtimeState.AddEvent(&backend.HistoryEvent{}); !assert.NoError(t, err) {
			return
		}
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)

	req, err := wfstate.GetSaveRequest("wf1")
	if assert.NoError(t, err) {
		assert.Equal(t, "wf1", req.ActorID)
		assert.Equal(t, wfengine.WorkflowActorType, req.ActorType)

		upsertCount, deleteCount := countOperations(t, req)
		assert.Equal(t, 12, upsertCount) // 10x history + metadata + customStatus
		assert.Equal(t, 0, deleteCount)
	}
}

func TestLoadSavedState(t *testing.T) {
	generation := uuid.New()
	wfstate := wfengine.NewWorkflowState(generation)

	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		if err := runtimeState.AddEvent(&backend.HistoryEvent{}); !assert.NoError(t, err) {
			return
		}
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)
	wfstate.CustomStatus = "my custom status"

	for i := 0; i < 5; i++ {
		wfstate.AddToInbox(&backend.HistoryEvent{})
	}

	req, err := wfstate.GetSaveRequest("wf1")
	if !assert.NoError(t, err) {
		return
	}

	upsertCount, deleteCount := countOperations(t, req)
	assert.Equal(t, 17, upsertCount) // 10x history, 5x inbox, 1 metadata, 1 customStatus
	assert.Equal(t, 0, deleteCount)

	actors := getActorRuntime()
	if err = actors.TransactionalStateOperation(context.Background(), req); !assert.NoError(t, err) {
		return
	}

	wfstate, err = wfengine.LoadWorkflowState(context.Background(), actors, "wf1")
	if assert.NoError(t, err) && assert.NotNil(t, wfstate) {
		assert.Equal(t, "my custom status", wfstate.CustomStatus)
		assert.Equal(t, generation, wfstate.Generation)
		assert.Equal(t, 10, len(wfstate.History))
		assert.Equal(t, 5, len(wfstate.Inbox))
	}
}

func TestResetLoadedState(t *testing.T) {
	wfstate := wfengine.NewWorkflowState(uuid.New())

	runtimeState := backend.NewOrchestrationRuntimeState(api.InstanceID("wf1"), nil)
	for i := 0; i < 10; i++ {
		if err := runtimeState.AddEvent(&backend.HistoryEvent{}); !assert.NoError(t, err) {
			return
		}
	}
	wfstate.ApplyRuntimeStateChanges(runtimeState)

	for i := 0; i < 5; i++ {
		wfstate.AddToInbox(&backend.HistoryEvent{})
	}

	req, err := wfstate.GetSaveRequest("wf1")
	if !assert.NoError(t, err) {
		return
	}

	actorRuntime := getActorRuntime()
	if err = actorRuntime.TransactionalStateOperation(context.Background(), req); !assert.NoError(t, err) {
		return
	}

	wfstate, err = wfengine.LoadWorkflowState(context.Background(), actorRuntime, "wf1")
	if assert.NoError(t, err) && assert.NotNil(t, wfstate) {
		wfstate.Reset()
		req, err := wfstate.GetSaveRequest("wf1")
		if assert.NoError(t, err) {
			assert.Equal(t, 17, len(req.Operations)) // history x10 + inbox x5 + metadata + customStatus
			upsertCount, deleteCount := countOperations(t, req)
			assert.Equal(t, 2, upsertCount)  // metadata + customStatus
			assert.Equal(t, 15, deleteCount) // all history and inbox records are deleted
		}
	}
}

func getActorRuntime() actors.Actors {
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:              testAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	actors := actors.NewActors(actors.ActorsOpts{
		StateStore:     store,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  NewMockPlacement(),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})
	return actors
}

func countOperations(t *testing.T, req *actors.TransactionalRequest) (int, int) {
	upsertCount := 0
	deleteCount := 0
	for _, op := range req.Operations {
		if op.Operation == actors.Upsert {
			upsertCount++
		} else if op.Operation == actors.Delete {
			deleteCount++
		} else {
			assert.Fail(t, "unexpected operation type", op.Operation)
		}
	}
	return upsertCount, deleteCount
}
