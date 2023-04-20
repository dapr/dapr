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
package wfengine

import (
	"context"
	"encoding/json"
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

const (
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	GetWorkflowMetadataMethod    = "GetWorkflowMetadata"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
	PurgeWorkflowStateMethod     = "PurgeWorkflowState"
)

type workflowActor struct {
	actors           actors.Actors
	states           sync.Map
	scheduler        workflowScheduler
	cachingDisabled  bool
	defaultTimeout   time.Duration
	reminderInterval time.Duration
	config           wfConfig
}

type durableTimer struct {
	Bytes      []byte `json:"bytes"`
	Generation uint64 `json:"generation"`
}

type recoverableError struct {
	cause error
}

func NewDurableTimer(bytes []byte, generation uint64) durableTimer {
	return durableTimer{bytes, generation}
}

func newRecoverableError(err error) recoverableError {
	return recoverableError{cause: err}
}

func (err recoverableError) Error() string {
	return err.cause.Error()
}

func NewWorkflowActor(scheduler workflowScheduler, config wfConfig) *workflowActor {
	return &workflowActor{
		scheduler:        scheduler,
		defaultTimeout:   30 * time.Second,
		reminderInterval: 1 * time.Minute,
		config:           config,
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
		result, err = wf.getWorkflowMetadata(ctx, actorID)
	case AddWorkflowEventMethod:
		err = wf.addWorkflowEvent(ctx, actorID, request)
	case PurgeWorkflowStateMethod:
		err = wf.purgeWorkflowState(ctx, actorID)
	default:
		err = fmt.Errorf("no such method: %s", methodName)
	}
	return result, err
}

// InvokeReminder implements actors.InternalActor
func (wf *workflowActor) InvokeReminder(ctx context.Context, actorID string, reminderName string, data []byte, dueTime string, period string) error {
	wfLogger.Debugf("invoking reminder '%s' on workflow actor '%s'", reminderName, actorID)

	// Workflow executions should never take longer than a few seconds at the most
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, wf.defaultTimeout)
	defer cancelTimeout()
	if err := wf.runWorkflow(timeoutCtx, actorID, reminderName, data); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			wfLogger.Warnf("%s: execution timed-out and will be retried later", actorID)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else if errors.Is(err, context.Canceled) {
			wfLogger.Warnf("%s: execution was canceled (process shutdown?) and will be retried later", actorID)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else if _, ok := err.(recoverableError); ok {
			wfLogger.Warnf("%s: execution failed with a recoverable error and will be retried later: %v", actorID, err)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else {
			wfLogger.Errorf("%s: execution failed with a non-recoverable error: %v", actorID, err)
		}
	}

	// We delete the reminder on success and on non-recoverable errors.
	return actors.ErrReminderCanceled
}

// InvokeTimer implements actors.InternalActor
func (wf *workflowActor) InvokeTimer(ctx context.Context, actorID string, timerName string, params []byte) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (wf *workflowActor) DeactivateActor(ctx context.Context, actorID string) error {
	wfLogger.Debugf("deactivating workflow actor '%s'", actorID)
	wf.states.Delete(actorID)
	return nil
}

func (wf *workflowActor) createWorkflowInstance(ctx context.Context, actorID string, startEventBytes []byte) error {
	// create a new state entry if one doesn't already exist
	state, exists, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	} else if !exists {
		state = NewWorkflowState(wf.config)
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
			state.Reset()
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

	state.AddToInbox(startEvent)
	return wf.saveInternalState(ctx, actorID, state)
}

func (wf *workflowActor) getWorkflowMetadata(ctx context.Context, actorID string) (*api.OrchestrationMetadata, error) {
	state, exists, err := wf.loadInternalState(ctx, actorID)
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
		state.CustomStatus,
		failureDetuils,
	)
	return metadata, nil
}

// This method purges all the completed activity data from a workflow associated with the given actorID
func (wf *workflowActor) purgeWorkflowState(ctx context.Context, actorID string) error {
	state, exists, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	} else if !exists {
		return api.ErrInstanceNotFound
	}

	runtimeState := getRuntimeState(actorID, state)
	if !runtimeState.IsCompleted() {
		return api.ErrNotCompleted
	}

	// Loop through the history of the loaded workflow state and schedule purge calls for all the activities that have been completed

	// RRL TODO: Looping through history and invoking each actor once. They are not in the same transaction. Each transaction happens as its own call
	// We are deleting one by one in sequence and not in builk.
	// In case of each actor returning an error, the entire function returns an error
	// Everything before error is deleted, and everything after is not.
	for _, e := range state.History {
		if ts := e.GetTaskScheduled(); ts != nil {
			targetActorID := getActivityActorID(actorID, e.EventId)
			activityRequestBytes, err := actors.EncodeInternalActorData(ActivityRequest{
				Generation: state.Generation,
			})
			if err != nil {
				return err
			}
			req := invokev1.
				NewInvokeMethodRequest(PurgeWorkflowStateMethod).
				WithActor(wf.config.activityActorType, targetActorID).
				WithRawDataBytes(activityRequestBytes).
				WithContentType(invokev1.OctetStreamContentType)

			resp, err := wf.actors.Call(ctx, req)
			req.Close()
			if err != nil {
				return fmt.Errorf("failed to purge activity actor '%s' ('%s'): %w", targetActorID, ts.Name, err)
			}
			resp.Close()
		}
	}

	// This will create a request to purge everything
	req, err := state.GetPurgeRequest(actorID)
	if err != nil {
		return err
	}
	// This will do the purging
	err = wf.actors.TransactionalStateOperation(ctx, req)
	if err != nil {
		return err
	}
	wf.states.Delete(actorID)
	return nil

}

func (wf *workflowActor) addWorkflowEvent(ctx context.Context, actorID string, historyEventBytes []byte) error {
	state, exists, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	} else if !exists {
		return api.ErrInstanceNotFound
	}

	e, err := backend.UnmarshalHistoryEvent(historyEventBytes)
	if err != nil {
		return err
	}
	state.AddToInbox(e)

	if _, err := wf.createReliableReminder(ctx, actorID, "new-event", nil, 0); err != nil {
		return err
	}
	return wf.saveInternalState(ctx, actorID, state)
}

func (wf *workflowActor) runWorkflow(ctx context.Context, actorID string, reminderName string, reminderData []byte) error {
	state, exists, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	} else if !exists {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		return errors.New("no workflow state found")
	}

	if strings.HasPrefix(reminderName, "timer-") {
		var timerData durableTimer
		if err = actors.DecodeInternalActorReminderData(reminderData, &timerData); err != nil {
			// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
			return err
		}
		if timerData.Generation < state.Generation {
			wfLogger.Infof("%s: ignoring durable timer from previous generation '%v'", actorID, timerData.Generation)
			return nil
		} else {
			e, err := backend.UnmarshalHistoryEvent(timerData.Bytes)
			if err != nil {
				// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
				return fmt.Errorf("failed to unmarshal timer data %w", err)
			}
			state.Inbox = append(state.Inbox, e)
		}
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		wfLogger.Debugf("%s: ignoring run request for reminder '%s' because the workflow inbox is empty", reminderName, actorID)
		return nil
	}

	runtimeState := getRuntimeState(actorID, state)
	wi := &backend.OrchestrationWorkItem{
		InstanceID: runtimeState.InstanceID(),
		NewEvents:  state.Inbox,
		RetryCount: -1, // TODO
		State:      runtimeState,
		Properties: make(map[string]interface{}),
	}

	// Executing workflow code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	callback := make(chan bool)
	wi.Properties[CallbackChannelProperty] = callback
	if err := wf.scheduler.ScheduleWorkflow(ctx, wi); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return newRecoverableError(errors.New(
				"timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running"))
		}
		return newRecoverableError(fmt.Errorf("failed to schedule a workflow execution: %w", err))
	}

	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		return ctx.Err()
	case completed := <-callback:
		if !completed {
			return newRecoverableError(errExecutionAborted)
		}
	}

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if runtimeState.ContinuedAsNew() {
		state.Generation += 1
	}

	if !runtimeState.IsCompleted() {
		// Create reminders for the durable timers. We only do this if the orchestration is still running.
		for _, t := range runtimeState.PendingTimers() {
			tf := t.GetTimerFired()
			if tf == nil {
				return errors.New("invalid event in the PendingTimers list")
			}
			timerBytes, err := backend.MarshalHistoryEvent(t)
			if err != nil {
				return fmt.Errorf("failed to marshal pending timer data: %w", err)
			}
			delay := tf.FireAt.AsTime().Sub(time.Now().UTC())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := fmt.Sprintf("timer-%d", tf.TimerId)
			data := NewDurableTimer(timerBytes, state.Generation)
			if _, err := wf.createReliableReminder(ctx, actorID, reminderPrefix, data, delay); err != nil {
				return newRecoverableError(fmt.Errorf("actor %s failed to create reminder for timer: %w", actorID, err))
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
			activityRequestBytes, err := actors.EncodeInternalActorData(ActivityRequest{
				HistoryEvent: eventData,
				Generation:   state.Generation,
			})
			if err != nil {
				return err
			}
			targetActorID := getActivityActorID(actorID, e.EventId)

			req := invokev1.
				NewInvokeMethodRequest("Execute").
				WithActor(wf.config.activityActorType, targetActorID).
				WithRawDataBytes(activityRequestBytes).
				WithContentType(invokev1.OctetStreamContentType)
			defer req.Close()

			resp, err := wf.actors.Call(ctx, req)
			if err != nil {
				if errors.Is(err, ErrDuplicateInvocation) {
					wfLogger.Warnf("%s: activity invocation %s#%d was flagged as a duplicate and will be skipped", actorID, ts.Name, e.EventId)
					continue
				}
				return newRecoverableError(fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %v", targetActorID, ts.Name, err))
			}
			resp.Close()
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
				WithActor(wf.config.workflowActorType, msg.TargetInstanceID).
				WithRawDataBytes(eventData).
				WithContentType(invokev1.OctetStreamContentType)
			defer req.Close()

			resp, err := wf.actors.Call(ctx, req)
			if err != nil {
				// workflow-related actor methods are never expected to return errors
				return newRecoverableError(
					fmt.Errorf("method %s on actor '%s' returned an error: %w", method, msg.TargetInstanceID, err))
			}
			defer resp.Close()
		}
	}

	state.ApplyRuntimeStateChanges(runtimeState)
	state.ClearInbox()

	return wf.saveInternalState(ctx, actorID, state)
}

func (wf *workflowActor) loadInternalState(ctx context.Context, actorID string) (workflowState, bool, error) {
	// see if the state for this actor is already cached in memory
	cachedState, ok := wf.states.Load(actorID)
	if ok {
		return cachedState.(workflowState), true, nil
	}

	// state is not cached, so try to load it from the state store
	wfLogger.Debugf("%s: loading workflow state", actorID)
	state, err := LoadWorkflowState(ctx, wf.actors, actorID, wf.config)
	if err != nil {
		return workflowState{}, false, err
	}
	if state.Generation == 0 {
		// No such state exists in the state store
		return workflowState{}, false, nil
	}
	return state, true, nil
}

func (wf *workflowActor) saveInternalState(ctx context.Context, actorID string, state workflowState) error {
	if !wf.cachingDisabled {
		// update cached state
		wf.states.Store(actorID, state)
	}

	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(actorID)
	if err != nil {
		return err
	}

	wfLogger.Debugf("%s: saving %d keys to actor state store", actorID, len(req.Operations))
	if err = wf.actors.TransactionalStateOperation(ctx, req); err != nil {
		return err
	}
	return nil
}

func (wf *workflowActor) createReliableReminder(ctx context.Context, actorID string, namePrefix string, data any, delay time.Duration) (string, error) {
	// Reminders need to have unique names or else they may not fire in certain race conditions.
	reminderName := fmt.Sprintf("%s-%s", namePrefix, uuid.NewString()[:8])
	wfLogger.Debugf("%s: creating '%s' reminder with DueTime = %s", actorID, reminderName, delay)
	dataEnc, err := json.Marshal(data)
	if err != nil {
		return reminderName, fmt.Errorf("failed to encode data as JSON: %w", err)
	}

	return reminderName, wf.actors.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: wf.config.workflowActorType,
		ActorID:   actorID,
		Data:      dataEnc,
		DueTime:   delay.String(),
		Name:      reminderName,
		Period:    wf.reminderInterval.String(),
	})
}

func getRuntimeState(actorID string, state workflowState) *backend.OrchestrationRuntimeState {
	// TODO: Add caching when a good invalidation policy can be determined
	return backend.NewOrchestrationRuntimeState(api.InstanceID(actorID), state.History)
}

func getActivityActorID(workflowActorID string, taskID int32) string {
	// An activity can be identified by it's name followed by it's task ID. Example: SayHello#0, SayHello#1, etc.
	return fmt.Sprintf("%s#%d", workflowActorID, taskID)
}
