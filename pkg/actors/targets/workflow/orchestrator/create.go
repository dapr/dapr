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

package orchestrator

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

func (o *orchestrator) createWorkflowInstance(ctx context.Context, request []byte) error {
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
			log.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'",
				o.actorID,
				es.GetName(),
				es.GetOrchestrationInstance().GetInstanceId(),
			)
		} else {
			log.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'",
				o.actorID,
				es.GetName(),
				es.GetOrchestrationInstance().GetInstanceId(),
				es.GetParentInstance().GetName(),
				es.GetParentInstance().GetOrchestrationInstance().GetInstanceId(),
			)
		}
	}

	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	// orchestration didn't exist
	// create a new state entry if one doesn't already exist
	if state == nil {
		state = wfenginestate.NewState(wfenginestate.Options{
			AppID:             o.appID,
			WorkflowActorType: o.actorType,
			ActivityActorType: o.activityActorType,
		})
		o.rstate = runtimestate.NewOrchestrationRuntimeState(o.actorID, state.CustomStatus, state.History)
		o.ometa = o.ometaFromState(o.rstate, startEvent.GetExecutionStarted())
		return o.scheduleWorkflowStart(ctx, startEvent, state)
	}

	// orchestration already existed: apply reuse id policy
	rs := o.rstate
	runtimeStatus := runtimestate.RuntimeStatus(rs)
	// if target status doesn't match, fall back to original logic, create instance only if previous one is completed
	if !isStatusMatch(reuseIDPolicy.GetOperationStatus(), runtimeStatus) {
		return o.createIfCompleted(ctx, rs, state, startEvent)
	}

	switch reuseIDPolicy.GetAction() {
	case api.REUSE_ID_ACTION_IGNORE:
		// Log an warning message and ignore creating new instance
		log.Warnf("Workflow actor '%s': ignoring request to recreate the current workflow instance", o.actorID)
		return nil
	case api.REUSE_ID_ACTION_TERMINATE:
		// terminate existing instance
		if err := o.cleanupWorkflowStateInternal(ctx, state, false); err != nil {
			return fmt.Errorf("failed to terminate existing instance with ID '%s'", o.actorID)
		}

		// created a new instance
		state.Reset()
		return o.scheduleWorkflowStart(ctx, startEvent, state)
	}
	// default Action ERROR, fall back to original logic
	return o.createIfCompleted(ctx, rs, state, startEvent)
}

func (o *orchestrator) createIfCompleted(ctx context.Context, rs *backend.OrchestrationRuntimeState, state *wfenginestate.State, startEvent *backend.HistoryEvent) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimestate.IsCompleted(rs) {
		// This happens when the parent's runWorkflow created the child workflow
		// successfully but crashed before persisting its own state, causing it to
		// re-execute and attempt the child creation again.
		if o.isSameParentCreation(state, startEvent) {
			log.Debugf("Workflow actor '%s': ignoring duplicate child workflow creation from parent '%s'",
				o.actorID, startEvent.GetExecutionStarted().GetParentInstance().GetOrchestrationInstance().GetInstanceId())
			return nil
		}
		return fmt.Errorf("an active workflow with ID '%s' already exists", o.actorID)
	}
	if o.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", o.actorID)
	}
	log.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", o.actorID)
	state.Reset()
	return o.scheduleWorkflowStart(ctx, startEvent, state)
}

func (o *orchestrator) scheduleWorkflowStart(ctx context.Context, startEvent *backend.HistoryEvent, state *wfenginestate.State) error {
	start := startEvent.GetTimestamp().AsTime()
	if ts := startEvent.GetExecutionStarted().GetScheduledStartTimestamp(); ts != nil {
		start = ts.AsTime()
	}

	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := o.createWorkflowReminder(ctx, reminderPrefixStart, nil, start, o.appID); err != nil {
		return err
	}
	state.AddToInbox(startEvent)
	if err := o.saveInternalState(ctx, state); err != nil {
		return err
	}

	return nil
}

func (o *orchestrator) isSameParentCreation(state *wfenginestate.State, startEvent *backend.HistoryEvent) bool {
	newParent := startEvent.GetExecutionStarted().GetParentInstance()
	if newParent == nil {
		return false
	}

	existingParent := o.getExecutionStartedEvent(state).GetParentInstance()
	if existingParent == nil {
		return false
	}

	return existingParent.GetOrchestrationInstance().GetInstanceId() == newParent.GetOrchestrationInstance().GetInstanceId() &&
		existingParent.GetTaskScheduledId() == newParent.GetTaskScheduledId()
}

func isStatusMatch(statuses []api.OrchestrationStatus, runtimeStatus api.OrchestrationStatus) bool {
	for _, status := range statuses {
		if status == runtimeStatus {
			return true
		}
	}
	return false
}
