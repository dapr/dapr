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
package wfengine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
)

type workflowActor struct {
	actorRuntime actors.Actors
	states       map[string]*workflowState
	scheduler    workflowScheduler
}

type workflowState struct {
	inbox   []*backend.HistoryEvent
	history []*backend.HistoryEvent

	// runtimeState is a cached copy of the [backend.OrchestrationRuntimeState]
	runtimeState *backend.OrchestrationRuntimeState
}

const (
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	GetWorkflowMetadataMethod    = "GetWorkflowMetadata"

	createWorkflowReminder = "start" // internal reminder for executing workflows
)

func NewWorkflowActor(scheduler workflowScheduler) actors.InternalActor {
	return &workflowActor{
		states:    make(map[string]*workflowState),
		scheduler: scheduler,
	}
}

// SetActorRuntime implements actors.InternalActor
func (wf *workflowActor) SetActorRuntime(actorRuntime actors.Actors) {
	wf.actorRuntime = actorRuntime
}

// InvokeMethod implements actors.InternalActor
func (wf *workflowActor) InvokeMethod(ctx context.Context, actorID string, methodName string, request []byte) (interface{}, error) {
	wfLogger.Debugf("invoking method '%s' on workflow actor '%s'", methodName, actorID)

	var result interface{}
	var err error

	switch methodName {
	case CreateWorkflowInstanceMethod:
		err = wf.createWorkflowInstanceMethod(ctx, actorID, request)
	case GetWorkflowMetadataMethod:
		result, err = wf.getWorkflowMetadata(actorID)
	default:
		err = fmt.Errorf("no such method: %s", methodName)
	}
	return result, err
}

// InvokeReminder implements actors.InternalActor
func (wf *workflowActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, params []byte) error {
	wfLogger.Debugf("invoking reminder '%s' on workflow actor '%s'", reminderName, actorID)

	var err error
	switch reminderName {
	case createWorkflowReminder:
		// Workflow executions should never take longer than a few seconds at the most
		timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second) // TODO: Configurable
		defer cancelTimeout()
		err = wf.runWorkflow(timeoutCtx, actorID)
	default:
		wfLogger.Warnf("reminder '%s' for workflow actor '%s' was not recognized", reminderName, actorID)
	}

	return err
}

// InvokeTimer implements actors.InternalActor
func (wf *workflowActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	wfLogger.Debugf("invoking timer '%s' on workflow actor '%s'", timerName, actorID)
	// TODO
	return nil
}

// DeactivateActor implements actors.InternalActor
func (wf *workflowActor) DeactivateActor(ctx context.Context, actorID string) error {
	wfLogger.Debugf("deactivating workflow actor '%s'", actorID)
	delete(wf.states, actorID)
	return nil
}

func (wf *workflowActor) createWorkflowInstanceMethod(ctx context.Context, actorID string, startEventBytes []byte) error {
	// create a new state entry if one doesn't already exist
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return err
	} else if !exists {
		state = &workflowState{
			inbox: []*backend.HistoryEvent{},
		}
	}

	runtimeState := getAndCacheRuntimeState(actorID, state)
	if exists && !runtimeState.IsCompleted() {
		return errors.New("workflow instance with this ID already exists")
	}

	startEvent, err := backend.UnmarshalHistoryEvent(startEventBytes)
	if err != nil {
		return err
	} else if startEvent.GetExecutionStarted() == nil {
		return errors.New("invalid execution start event")
	}

	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if err := wf.createReliableReminder(ctx, actorID, createWorkflowReminder, nil, 0); err != nil {
		return err
	}

	state.inbox = []*backend.HistoryEvent{startEvent}
	return wf.saveInternalState(actorID, state)
}

func (wf *workflowActor) getWorkflowMetadata(actorID string) (*api.OrchestrationMetadata, error) {
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return nil, err
	} else if !exists {
		return nil, api.ErrInstanceNotFound
	}

	runtimeState := getAndCacheRuntimeState(actorID, state)

	name, _ := runtimeState.Name()
	createdAt, _ := runtimeState.CreatedTime()
	lastUpdated, _ := runtimeState.LastUpdatedTime()
	input, _ := runtimeState.Input()
	output, _ := runtimeState.Output()
	failureDetuils, _ := runtimeState.FailureDetails()

	metadata := api.NewOrchestrationMetadata(
		runtimeState.InstanceID(),
		name,
		runtimeState.RuntimeStatus(),
		createdAt,
		lastUpdated,
		input,
		output,
		runtimeState.CustomStatus.GetValue(),
		failureDetuils,
	)
	return metadata, nil
}

func (wf *workflowActor) runWorkflow(ctx context.Context, actorID string) error {
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("no workflow state found for actor '%s'", actorID)
	} else if len(state.inbox) == 0 {
		// This is never expected - all run requests should be triggered by some inbox event
		wfLogger.Warnf("%s: ignoring run request for workflow with empty inbox", actorID)
	}

	runtimeState := getAndCacheRuntimeState(actorID, state)
	wi := &backend.OrchestrationWorkItem{
		InstanceID: runtimeState.InstanceID(),
		NewEvents:  state.inbox,
		RetryCount: -1, // TODO
		State:      runtimeState,
		Properties: make(map[string]interface{}),
	}

	// Executing workflow code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	callback := make(chan bool)
	wi.Properties[CallbackChannelProperty] = callback
	wf.scheduler.ScheduleWorkflow(wi)
	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		return ctx.Err()
	case <-callback:
	}

	// TODO: Go through the pending actions

	// Clear the inbox
	state.inbox = nil
	return wf.saveInternalState(actorID, state)
}

func (wf *workflowActor) loadInternalState(actorID string) (*workflowState, bool, error) {
	// see if the state for this actor is already cached in memory
	state, ok := wf.states[actorID]
	if ok {
		return state, true, nil
	}

	// state is not cached, so try to load it from the state store
	wfLogger.Debugf("%s: loading workflow state", actorID)
	return nil, false, nil // TODO: Implement fetching from storage after validating saving to storage
}

func (wf *workflowActor) saveInternalState(actorID string, state *workflowState) error {
	wf.states[actorID] = state
	return nil // TODO: Implement persistence this after validating reminders
}

func (wf *workflowActor) createReliableReminder(ctx context.Context, actorID string, name string, data any, delay time.Duration) error {
	wfLogger.Debugf("%s: creating '%s' reminder with DueTime = %s", actorID, name, delay)
	return wf.actorRuntime.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: WorkflowActorType,
		ActorID:   actorID,
		Data:      data,
		DueTime:   delay.String(),
		Name:      name,
		Period:    "",
	})
}

func getAndCacheRuntimeState(actorID string, state *workflowState) *backend.OrchestrationRuntimeState {
	if state.runtimeState == nil {
		state.runtimeState = backend.NewOrchestrationRuntimeState(api.InstanceID(actorID), state.history)
	}
	return state.runtimeState
}
