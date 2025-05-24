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
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (w *workflow) createWorkflowInstance(ctx context.Context, request []byte) error {
	var createWorkflowInstanceRequest backend.CreateWorkflowInstanceRequest
	if err := proto.Unmarshal(request, &createWorkflowInstanceRequest); err != nil {
		return fmt.Errorf("failed to unmarshal createWorkflowInstanceRequest: %w", err)
	}
	reuseIDPolicy := createWorkflowInstanceRequest.GetPolicy()

	startEvent := createWorkflowInstanceRequest.GetStartEvent()
	if es := startEvent.GetExecutionStarted(); es == nil {
		return errors.New("invalid execution start event")
	} else {
		if es.GetParentInstance() == nil {
			log.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'", w.actorID, es.GetName(), es.GetOrchestrationInstance().GetInstanceId())
		} else {
			log.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'", es.GetName(), es.GetOrchestrationInstance().GetInstanceId(), es.GetParentInstance().GetName(), es.GetParentInstance().GetOrchestrationInstance().GetInstanceId())
		}
	}

	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}

	// orchestration didn't exist
	// create a new state entry if one doesn't already exist
	if state == nil {
		state = wfenginestate.NewState(wfenginestate.Options{
			AppID:             w.appID,
			WorkflowActorType: w.actorType,
			ActivityActorType: w.activityActorType,
		})
		w.lock.Lock()
		w.rstate = runtimestate.NewOrchestrationRuntimeState(w.actorID, state.CustomStatus, state.History)
		w.setOrchestrationMetadata(w.rstate, startEvent.GetExecutionStarted())
		w.lock.Unlock()
		return w.scheduleWorkflowStart(ctx, startEvent, state)
	}

	// orchestration already existed: apply reuse id policy
	rs := w.rstate
	runtimeStatus := runtimestate.RuntimeStatus(rs)
	// if target status doesn't match, fall back to original logic, create instance only if previous one is completed
	if !isStatusMatch(reuseIDPolicy.GetOperationStatus(), runtimeStatus) {
		return w.createIfCompleted(ctx, rs, state, startEvent)
	}

	switch reuseIDPolicy.GetAction() {
	case api.REUSE_ID_ACTION_IGNORE:
		// Log an warning message and ignore creating new instance
		log.Warnf("Workflow actor '%s': ignoring request to recreate the current workflow instance", w.actorID)
		return nil
	case api.REUSE_ID_ACTION_TERMINATE:
		// terminate existing instance
		if err := w.cleanupWorkflowStateInternal(ctx, state, false); err != nil {
			return fmt.Errorf("failed to terminate existing instance with ID '%s'", w.actorID)
		}

		// created a new instance
		state.Reset()
		return w.scheduleWorkflowStart(ctx, startEvent, state)
	}
	// default Action ERROR, fall back to original logic
	return w.createIfCompleted(ctx, rs, state, startEvent)
}

func (w *workflow) createIfCompleted(ctx context.Context, rs *backend.OrchestrationRuntimeState, state *wfenginestate.State, startEvent *backend.HistoryEvent) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimestate.IsCompleted(rs) {
		return fmt.Errorf("an active workflow with ID '%s' already exists", w.actorID)
	}
	if w.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", w.actorID)
	}
	log.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", w.actorID)
	state.Reset()
	return w.scheduleWorkflowStart(ctx, startEvent, state)
}

func (w *workflow) scheduleWorkflowStart(ctx context.Context, startEvent *backend.HistoryEvent, state *wfenginestate.State) error {
	state.AddToInbox(startEvent)
	if err := w.saveInternalState(ctx, state); err != nil {
		return err
	}

	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := w.createReminder(ctx, "start", nil, 0); err != nil {
		return err
	}

	return nil
}

func isStatusMatch(statuses []api.OrchestrationStatus, runtimeStatus api.OrchestrationStatus) bool {
	for _, status := range statuses {
		if status == runtimeStatus {
			return true
		}
	}
	return false
}
