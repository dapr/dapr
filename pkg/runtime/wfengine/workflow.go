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
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

type workflowActor struct {
	actors    actors.Actors
	states    map[string]*workflowState
	scheduler workflowScheduler
	rwLock    sync.RWMutex
}

type workflowState struct {
	inbox        []*backend.HistoryEvent
	history      []*backend.HistoryEvent
	timers       map[string]*backend.HistoryEvent
	customStatus string
}

const (
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	GetWorkflowMetadataMethod    = "GetWorkflowMetadata"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
)

func NewWorkflowActor(scheduler workflowScheduler) actors.InternalActor {
	return &workflowActor{
		states:    make(map[string]*workflowState),
		scheduler: scheduler,
	}
}

// SetActorRuntime implements actors.InternalActor
func (wf *workflowActor) SetActorRuntime(actorRuntime actors.Actors) {
	wf.actors = actorRuntime
}

// InvokeMethod implements actors.InternalActor
func (wf *workflowActor) InvokeMethod(ctx context.Context, actorID string, methodName string, request []byte) (interface{}, error) {
	wfLogger.Debugf("invoking method '%s' on workflow actor '%s'", methodName, actorID)

	var result interface{}
	var err error

	switch methodName {
	case CreateWorkflowInstanceMethod:
		err = wf.createWorkflowInstance(ctx, actorID, request)
	case GetWorkflowMetadataMethod:
		result, err = wf.getWorkflowMetadata(actorID)
	case AddWorkflowEventMethod:
		err = wf.addWorkflowEvent(ctx, actorID, request)
	default:
		err = fmt.Errorf("no such method: %s", methodName)
	}
	return result, err
}

// InvokeReminder implements actors.InternalActor
func (wf *workflowActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, data any, dueTime string, period string) error {
	wfLogger.Debugf("invoking reminder '%s' on workflow actor '%s'", reminderName, actorID)

	// Workflow executions should never take longer than a few seconds at the most
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second) // TODO: Configurable
	defer cancelTimeout()
	if err := wf.runWorkflow(timeoutCtx, actorID, reminderName); err != nil {
		return err
	}

	return nil
}

// InvokeTimer implements actors.InternalActor
func (wf *workflowActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	return nil // We don't use timers
}

// DeactivateActor implements actors.InternalActor
func (wf *workflowActor) DeactivateActor(ctx context.Context, actorID string) error {
	wf.rwLock.Lock()
	defer wf.rwLock.Unlock()

	wfLogger.Debugf("deactivating workflow actor '%s'", actorID)
	delete(wf.states, actorID)
	return nil
}

func (wf *workflowActor) createWorkflowInstance(ctx context.Context, actorID string, startEventBytes []byte) error {
	// create a new state entry if one doesn't already exist
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return err
	} else if !exists {
		state = &workflowState{}
	}

	startEvent, err := backend.UnmarshalHistoryEvent(startEventBytes)
	if err != nil {
		return err
	} else if startEvent.GetExecutionStarted() == nil {
		return errors.New("invalid execution start event")
	}

	// We block (re)creation of existing workflows unless they are in a completed state.
	if exists {
		runtimeState := getRuntimeState(actorID, state)
		if runtimeState.IsCompleted() {
			state = &workflowState{}
		} else {
			return fmt.Errorf("an active workflow with ID '%s' already exists", actorID)
		}
	}

	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := wf.createReliableReminder(ctx, actorID, "start", nil, 0); err != nil {
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

	runtimeState := getRuntimeState(actorID, state)

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
		state.customStatus,
		failureDetuils,
	)
	return metadata, nil
}

func (wf *workflowActor) addWorkflowEvent(ctx context.Context, actorID string, historyEventBytes []byte) error {
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return err
	} else if !exists {
		return api.ErrInstanceNotFound
	}

	e, err := backend.UnmarshalHistoryEvent(historyEventBytes)
	if err != nil {
		return err
	}
	state.inbox = append(state.inbox, e)

	if _, err := wf.createReliableReminder(ctx, actorID, "new-event", nil, 0); err != nil {
		return err
	}
	return wf.saveInternalState(actorID, state)
}

func (wf *workflowActor) runWorkflow(ctx context.Context, actorID string, reminderName string) error {
	state, exists, err := wf.loadInternalState(actorID)
	if err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("no workflow state found for actor '%s'", actorID)
	}

	if strings.HasPrefix(reminderName, "timer-") {
		if t, ok := state.timers[reminderName]; ok {
			state.inbox = append(state.inbox, t)
			delete(state.timers, reminderName)
		} else {
			return fmt.Errorf("%s: no durable timer named '%s' found in workflow state", actorID, reminderName)
		}
	}

	if len(state.inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		wfLogger.Debugf("%s: ignoring run request for reminder '%s' because the workflow inbox is empty", reminderName, actorID)
		return nil
	}

	runtimeState := getRuntimeState(actorID, state)
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
	if err := wf.scheduler.ScheduleWorkflow(ctx, wi); err != nil {
		return fmt.Errorf("failed to schedule a workflow execution: %w", err)
	}

	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		return ctx.Err()
	case <-callback:
	}

	if !runtimeState.IsCompleted() {
		// Create reminders for the durable timers. We only do this if the orchestration is still running.
		for _, t := range runtimeState.PendingTimers() {
			if state.timers == nil {
				state.timers = make(map[string]*backend.HistoryEvent, len(runtimeState.PendingTimers()))
			}
			tf := t.GetTimerFired()
			if tf == nil {
				return errors.New("invalid event in the PendingTimers list")
			}
			delay := tf.FireAt.AsTime().Sub(time.Now().UTC())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := fmt.Sprintf("timer-%d", tf.TimerId)
			if reminderName, err := wf.createReliableReminder(ctx, actorID, reminderPrefix, nil, delay); err != nil {
				// TODO: This needs to be a retriable error
				return fmt.Errorf("actor %s failed to create reminder for timer: %w", actorID, err)
			} else {
				state.timers[reminderName] = t
			}
		}
	}

	// Process the outbound orchestrator events
	reqsByName := make(map[string][]backend.OrchestratorMessage, len(runtimeState.PendingMessages()))
	for _, msg := range runtimeState.PendingMessages() {
		if es := msg.HistoryEvent.GetExecutionStarted(); es != nil {
			reqsByName[CreateWorkflowInstanceMethod] = append(reqsByName[CreateWorkflowInstanceMethod], msg)
		} else if msg.HistoryEvent.GetSubOrchestrationInstanceCompleted() != nil || msg.HistoryEvent.GetSubOrchestrationInstanceFailed() != nil {
			reqsByName[AddWorkflowEventMethod] = append(reqsByName[AddWorkflowEventMethod], msg)
		} else {
			wfLogger.Warn("don't know how to process outbound message %v", msg)
		}
	}

	// Schedule activities (TODO: Parallelism)
	for _, e := range runtimeState.PendingTasks() {
		if ts := e.GetTaskScheduled(); ts != nil {
			eventData, err := backend.MarshalHistoryEvent(e)
			if err != nil {
				return err
			}
			targetActorID := getActivityActorID(actorID, e.EventId)
			req := invokev1.
				NewInvokeMethodRequest("Execute").
				WithActor(ActivityActorType, targetActorID).
				WithRawData(eventData, invokev1.OctetStreamContentType)
			if _, err := wf.actors.Call(ctx, req); err != nil {
				return fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %v", targetActorID, ts.Name, err)
			}
		} else {
			wfLogger.Warn("don't know how to process task %v", e)
		}
	}

	// TODO: Do these in parallel?
	for method, msgList := range reqsByName {
		for _, msg := range msgList {
			eventData, err := backend.MarshalHistoryEvent(msg.HistoryEvent)
			if err != nil {
				return err
			}
			req := invokev1.
				NewInvokeMethodRequest(method).
				WithActor(WorkflowActorType, msg.TargetInstanceID).
				WithRawData(eventData, invokev1.OctetStreamContentType)
			if _, err := wf.actors.Call(ctx, req); err != nil {
				return err
			}
		}
	}

	// If the workflow continue-as-new'd, we need to reset the history
	if runtimeState.ContinuedAsNew() {
		state.history = nil
	}

	// Update the workflow history
	for _, e := range runtimeState.NewEvents() {
		state.history = append(state.history, e)
	}

	// Clear the inbox since we should have processed all events successfully
	state.inbox = nil

	// Save any updates to the custom status
	state.customStatus = runtimeState.CustomStatus.GetValue()
	return wf.saveInternalState(actorID, state)
}

func (wf *workflowActor) loadInternalState(actorID string) (*workflowState, bool, error) {
	wf.rwLock.RLock()
	defer wf.rwLock.RUnlock()

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
	wf.rwLock.Lock()
	defer wf.rwLock.Unlock()

	wf.states[actorID] = state
	return nil // TODO: Implement persistence this after validating reminders
}

func (wf *workflowActor) createReliableReminder(ctx context.Context, actorID string, namePrefix string, data any, delay time.Duration) (string, error) {
	// Reminders need to have unique names or else they may not fire in certain race conditions.
	reminderName := fmt.Sprintf("%s-%s", namePrefix, uuid.NewString()[:8])
	wfLogger.Debugf("%s: creating '%s' reminder with DueTime = %s", actorID, reminderName, delay)
	return reminderName, wf.actors.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: WorkflowActorType,
		ActorID:   actorID,
		Data:      data,
		DueTime:   delay.String(),
		Name:      reminderName,
		Period:    "", // TODO: Add configurable period to enable durability
	})
}

func getRuntimeState(actorID string, state *workflowState) *backend.OrchestrationRuntimeState {
	// TODO: Add caching when a good invalidation policy can be determined
	return backend.NewOrchestrationRuntimeState(api.InstanceID(actorID), state.history)
}

func getActivityActorID(workflowActorID string, taskID int32) string {
	// An activity can be identified by it's name followed by it's task ID. Example: SayHello#0, SayHello#1, etc.
	return fmt.Sprintf("%s#%d", workflowActorID, taskID)
}
