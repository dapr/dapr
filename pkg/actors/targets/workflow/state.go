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

package workflow

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (w *workflow) loadInternalState(ctx context.Context) (*wfenginestate.State, *backend.OrchestrationMetadata, error) {
	// See if the state for this actor is already cached in memory
	if w.state != nil {
		return w.state, w.ometa, nil
	}

	// state is not cached, so try to load it from the state store
	log.Debugf("Workflow actor '%s': loading workflow state", w.actorID)
	state, err := wfenginestate.LoadWorkflowState(ctx, w.actorState, w.actorID, wfenginestate.Options{
		AppID:             w.appID,
		WorkflowActorType: w.actorType,
		ActivityActorType: w.activityActorType,
	})
	if err != nil {
		return nil, nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil, nil
	}
	// Update cached state
	w.lock.Lock()
	defer w.lock.Unlock()
	w.state = state
	w.rstate = runtimestate.NewOrchestrationRuntimeState(w.actorID, state.CustomStatus, state.History)
	w.setOrchestrationMetadata(w.rstate, w.getExecutionStartedEvent(state))
	w.ometaBroadcaster.Broadcast(w.ometa)

	return state, w.ometa, nil
}

func (w *workflow) saveInternalState(ctx context.Context, state *wfenginestate.State) error {
	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(w.actorID)
	if err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': saving %d keys to actor state store", w.actorID, len(req.Operations))

	if err = w.actorState.TransactionalStateOperation(ctx, true, req, false); err != nil {
		return err
	}

	// ResetChangeTracking should always be called after a save operation succeeds
	state.ResetChangeTracking()

	// Update cached state
	w.lock.Lock()
	defer w.lock.Unlock()
	w.state = state
	w.rstate = runtimestate.NewOrchestrationRuntimeState(w.actorID, state.CustomStatus, state.History)
	w.setOrchestrationMetadata(w.rstate, w.getExecutionStartedEvent(state))
	w.ometaBroadcaster.Broadcast(w.ometa)
	return nil
}

// This method cleans up a workflow associated with the given actorID
func (w *workflow) cleanupWorkflowStateInternal(ctx context.Context, state *wfenginestate.State, requiredAndNotCompleted bool) error {
	// If the workflow is required to complete but it's not yet completed then return [ErrNotCompleted]
	// This check is used by purging workflow
	if requiredAndNotCompleted {
		return api.ErrNotCompleted
	}

	// This will create a request to purge everything
	req, err := state.GetPurgeRequest(w.actorID)
	if err != nil {
		return err
	}

	// This will do the purging
	err = w.actorState.TransactionalStateOperation(ctx, true, req, false)
	if err != nil {
		return err
	}

	w.table.DeleteFromTableIn(w, 0)
	w.cleanup()

	return nil
}

func (w *workflow) setOrchestrationMetadata(rstate *backend.OrchestrationRuntimeState, startEvent *protos.ExecutionStartedEvent) {
	var se *protos.ExecutionStartedEvent = nil
	if rstate.GetStartEvent() != nil {
		se = rstate.GetStartEvent()
	} else if startEvent != nil {
		se = startEvent
	}

	name, _ := runtimestate.Name(rstate)
	if name == "" && se != nil {
		name = se.GetName()
	}
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)
	completedAt, _ := runtimestate.CompletedTime(rstate)
	input, _ := runtimestate.Input(rstate)
	output, _ := runtimestate.Output(rstate)
	failureDetails, _ := runtimestate.FailureDetails(rstate)
	var parentInstanceID string
	if se != nil && se.GetParentInstance() != nil && se.GetParentInstance().GetOrchestrationInstance() != nil {
		parentInstanceID = se.GetParentInstance().GetOrchestrationInstance().GetInstanceId()
	}
	w.ometa = &backend.OrchestrationMetadata{
		InstanceId:       rstate.GetInstanceId(),
		Name:             name,
		RuntimeStatus:    runtimestate.RuntimeStatus(rstate),
		CreatedAt:        timestamppb.New(createdAt),
		LastUpdatedAt:    timestamppb.New(lastUpdated),
		CompletedAt:      timestamppb.New(completedAt),
		Input:            input,
		Output:           output,
		CustomStatus:     rstate.GetCustomStatus(),
		FailureDetails:   failureDetails,
		ParentInstanceId: parentInstanceID,
	}
}

func (w *workflow) cleanup() {
	w.lock.Lock()
	defer w.lock.Unlock()

	w.ometaBroadcaster.Close()
	w.state = nil // A bit of extra caution, shouldn't be necessary
	w.rstate = nil
	w.ometa = nil

	if w.closed.CompareAndSwap(false, true) {
		close(w.closeCh)
	}
}

// This method purges all the completed activity data from a workflow associated with the given actorID
func (w *workflow) purgeWorkflowState(ctx context.Context) error {
	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}
	w.completed.Store(true)
	return w.cleanupWorkflowStateInternal(ctx, state, !runtimestate.IsCompleted(w.rstate))
}

func (w *workflow) getExecutionStartedEvent(state *wfenginestate.State) *protos.ExecutionStartedEvent {
	for _, e := range state.History {
		if es := e.GetExecutionStarted(); es != nil {
			return es
		}
	}
	for _, e := range state.Inbox {
		if es := e.GetExecutionStarted(); es != nil {
			return es
		}
	}
	return &protos.ExecutionStartedEvent{}
}
