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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	actors                actors.Actors
	states                sync.Map
	scheduler             workflowScheduler
	cachingDisabled       bool
	defaultTimeout        time.Duration
	reminderInterval      time.Duration
	config                actorsBackendConfig
	activityResultAwaited atomic.Bool
}

type durableTimer struct {
	Bytes      []byte `json:"bytes"`
	Generation uint64 `json:"generation"`
}

type recoverableError struct {
	cause error
}

type CreateWorkflowInstanceRequest struct {
	Policy          *api.OrchestrationIdReusePolicy `json:"policy"`
	StartEventBytes []byte                          `json:"startEventBytes"`
}

// workflowScheduler is a func interface for pushing workflow (orchestration) work items into the backend
type workflowScheduler func(ctx context.Context, wi *backend.OrchestrationWorkItem) error

func NewDurableTimer(bytes []byte, generation uint64) durableTimer {
	return durableTimer{bytes, generation}
}

func newRecoverableError(err error) recoverableError {
	return recoverableError{cause: err}
}

func (err recoverableError) Error() string {
	return err.cause.Error()
}

func NewWorkflowActor(scheduler workflowScheduler, config actorsBackendConfig) *workflowActor {
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
func (wf *workflowActor) InvokeMethod(ctx context.Context, actorID string, methodName string, request []byte) (result any, err error) {
	wfLogger.Debugf("Workflow actor '%s': invoking method '%s'", actorID, methodName)

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
	wfLogger.Debugf("Workflow actor '%s': invoking reminder '%s'", actorID, reminderName)

	// Workflow executions should never take longer than a few seconds at the most
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, wf.defaultTimeout)
	defer cancelTimeout()
	err := wf.runWorkflow(timeoutCtx, actorID, reminderName, data)
	if err != nil {
		var re recoverableError
		if errors.Is(err, context.DeadlineExceeded) {
			wfLogger.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", actorID, err)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else if errors.Is(err, context.Canceled) {
			wfLogger.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", actorID, err)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else if errors.As(err, &re) {
			wfLogger.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", actorID, re)

			// Returning nil signals that we want the execution to be retried in the next period interval
			return nil
		} else {
			wfLogger.Errorf("Workflow actor '%s': execution failed with a non-recoverable error: %v", actorID, err)
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
	wfLogger.Debugf("Workflow actor '%s': deactivating", actorID)
	wf.states.Delete(actorID)
	return nil
}

func (wf *workflowActor) createWorkflowInstance(ctx context.Context, actorID string, request []byte) error {
	// create a new state entry if one doesn't already exist
	state, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	}

	created := false
	if state == nil {
		state = NewWorkflowState(wf.config)
		created = true
	}

	var createWorkflowInstanceRequest CreateWorkflowInstanceRequest
	if err = json.Unmarshal(request, &createWorkflowInstanceRequest); err != nil {
		return fmt.Errorf("failed to unmarshal createWorkflowInstanceRequest: %w", err)
	}
	reuseIDPolicy := createWorkflowInstanceRequest.Policy
	startEventBytes := createWorkflowInstanceRequest.StartEventBytes

	// Ensure that the start event payload is a valid durabletask execution-started event
	startEvent, err := backend.UnmarshalHistoryEvent(startEventBytes)
	if err != nil {
		return err
	}
	if es := startEvent.GetExecutionStarted(); es == nil {
		return errors.New("invalid execution start event")
	} else {
		if es.GetParentInstance() == nil {
			wfLogger.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'", actorID, es.GetName(), es.GetOrchestrationInstance().GetInstanceId())
		} else {
			wfLogger.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'", es.GetName(), es.GetOrchestrationInstance().GetInstanceId(), es.GetParentInstance().GetName(), es.GetParentInstance().GetOrchestrationInstance().GetInstanceId())
		}
	}

	// orchestration didn't exist and was just created
	if created {
		return wf.scheduleWorkflowStart(ctx, actorID, startEvent, state)
	}

	// orchestration already existed: apply reuse id policy
	runtimeState := getRuntimeState(actorID, state)
	runtimeStatus := runtimeState.RuntimeStatus()
	// if target status doesn't match, fall back to original logic, create instance only if previous one is completed
	if !isStatusMatch(reuseIDPolicy.GetOperationStatus(), runtimeStatus) {
		return wf.createIfCompleted(ctx, runtimeState, actorID, state, startEvent)
	}

	switch reuseIDPolicy.GetAction() {
	case api.REUSE_ID_ACTION_IGNORE:
		// Log an warning message and ignore creating new instance
		wfLogger.Warnf("Workflow actor '%s': ignoring request to recreate the current workflow instance", actorID)
		return nil
	case api.REUSE_ID_ACTION_TERMINATE:
		// terminate existing instance
		if err := wf.cleanupWorkflowStateInternal(ctx, actorID, state, false); err != nil {
			return fmt.Errorf("failed to terminate existing instance with ID '%s'", actorID)
		}

		// created a new instance
		state.Reset()
		return wf.scheduleWorkflowStart(ctx, actorID, startEvent, state)
	}
	// default Action ERROR, fall back to original logic
	return wf.createIfCompleted(ctx, runtimeState, actorID, state, startEvent)
}

func isStatusMatch(statuses []api.OrchestrationStatus, runtimeStatus api.OrchestrationStatus) bool {
	for _, status := range statuses {
		if status == runtimeStatus {
			return true
		}
	}
	return false
}

func (wf *workflowActor) createIfCompleted(ctx context.Context, runtimeState *backend.OrchestrationRuntimeState, actorID string, state *workflowState, startEvent *backend.HistoryEvent) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimeState.IsCompleted() {
		return fmt.Errorf("an active workflow with ID '%s' already exists", actorID)
	}
	if wf.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", actorID)
	}
	wfLogger.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", actorID)
	state.Reset()
	return wf.scheduleWorkflowStart(ctx, actorID, startEvent, state)
}

func (wf *workflowActor) scheduleWorkflowStart(ctx context.Context, actorID string, startEvent *backend.HistoryEvent, state *workflowState) error {
	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := wf.createReliableReminder(ctx, actorID, "start", nil, 0); err != nil {
		return err
	}

	state.AddToInbox(startEvent)
	return wf.saveInternalState(ctx, actorID, state)
}

// This method cleans up a workflow associated with the given actorID
func (wf *workflowActor) cleanupWorkflowStateInternal(ctx context.Context, actorID string, state *workflowState, requiredAndNotCompleted bool) error {
	// If the workflow is required to complete but it's not yet completed then return [ErrNotCompleted]
	// This check is used by purging workflow
	if requiredAndNotCompleted {
		return api.ErrNotCompleted
	}

	err := wf.removeCompletedStateData(ctx, state, actorID)
	if err != nil {
		return err
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

func (wf *workflowActor) getWorkflowMetadata(ctx context.Context, actorID string) (*api.OrchestrationMetadata, error) {
	state, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return nil, err
	}
	if state == nil {
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
	state, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}
	runtimeState := getRuntimeState(actorID, state)
	return wf.cleanupWorkflowStateInternal(ctx, actorID, state, !runtimeState.IsCompleted())
}

func (wf *workflowActor) addWorkflowEvent(ctx context.Context, actorID string, historyEventBytes []byte) error {
	state, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}

	e, err := backend.UnmarshalHistoryEvent(historyEventBytes)
	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		wf.activityResultAwaited.CompareAndSwap(true, false)
	}
	if err != nil {
		return err
	}
	wfLogger.Debugf("Workflow actor '%s': adding event '%v' to the workflow inbox", actorID, e)
	state.AddToInbox(e)

	if _, err := wf.createReliableReminder(ctx, actorID, "new-event", nil, 0); err != nil {
		return err
	}
	return wf.saveInternalState(ctx, actorID, state)
}

func (wf *workflowActor) runWorkflow(ctx context.Context, actorID string, reminderName string, reminderData []byte) error {
	state, err := wf.loadInternalState(ctx, actorID)
	if err != nil {
		return fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
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
			wfLogger.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", actorID, timerData.Generation)
			return nil
		} else {
			e, eventErr := backend.UnmarshalHistoryEvent(timerData.Bytes)
			if eventErr != nil {
				// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
				return fmt.Errorf("failed to unmarshal timer data %w", eventErr)
			}
			state.Inbox = append(state.Inbox, e)
		}
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		wfLogger.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", actorID, reminderName)
		return nil
	}

	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	transactionalRequests := make(map[string][]actors.TransactionalOperation)
	for _, e := range state.Inbox {
		var taskID int32
		if ts := e.GetTaskCompleted(); ts != nil {
			taskID = ts.GetTaskScheduledId()
		} else if tf := e.GetTaskFailed(); tf != nil {
			taskID = tf.GetTaskScheduledId()
		} else {
			continue
		}
		op := actors.TransactionalOperation{
			Operation: actors.Delete,
			Request: actors.TransactionalDelete{
				Key: activityStateKey,
			},
		}
		activityActorID := getActivityActorID(actorID, taskID, state.Generation)
		if transactionalRequests[activityActorID] == nil {
			transactionalRequests[activityActorID] = []actors.TransactionalOperation{op}
		} else {
			transactionalRequests[activityActorID] = append(transactionalRequests[activityActorID], op)
		}
	}
	// TODO: for optimization make multiple go routines and run them in parallel
	for activityActorID, operations := range transactionalRequests {
		err = wf.actors.TransactionalStateOperation(ctx, &actors.TransactionalRequest{
			ActorType:  wf.config.activityActorType,
			ActorID:    activityActorID,
			Operations: operations,
		})
		if err != nil {
			return fmt.Errorf("failed to delete activity state for activity actor '%s' with error: %w", activityActorID, err)
		}
	}

	runtimeState := getRuntimeState(actorID, state)
	wi := &backend.OrchestrationWorkItem{
		InstanceID: runtimeState.InstanceID(),
		NewEvents:  state.Inbox,
		RetryCount: -1, // TODO
		State:      runtimeState,
		Properties: make(map[string]any, 1),
	}

	// Executing workflow code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	callback := make(chan bool)
	wi.Properties[CallbackChannelProperty] = callback
	wfLogger.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	err = wf.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return newRecoverableError(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
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
	wfLogger.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", actorID, runtimeState.RuntimeStatus().String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if runtimeState.ContinuedAsNew() {
		wfLogger.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", actorID, wi.InstanceID)
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
			delay := time.Until(tf.GetFireAt().AsTime())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := fmt.Sprintf("timer-%d", tf.GetTimerId())
			data := NewDurableTimer(timerBytes, state.Generation)
			wfLogger.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer", actorID, reminderPrefix)
			if _, err := wf.createReliableReminder(ctx, actorID, reminderPrefix, data, delay); err != nil {
				return newRecoverableError(fmt.Errorf("actor '%s' failed to create reminder for timer: %w", actorID, err))
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
			wfLogger.Warnf("Workflow actor '%s': don't know how to process outbound message '%v'", actorID, msg)
		}
	}

	// Schedule activities
	// TODO: Parallelism
	for _, e := range runtimeState.PendingTasks() {
		ts := e.GetTaskScheduled()
		if ts == nil {
			wfLogger.Warnf("Workflow actor '%s': unable to process task '%v'", actorID, e)
			continue
		}

		eventData, err := backend.MarshalHistoryEvent(e)
		if err != nil {
			return err
		}
		activityRequestBytes, err := actors.EncodeInternalActorData(ActivityRequest{
			HistoryEvent: eventData,
		})
		if err != nil {
			return err
		}
		targetActorID := getActivityActorID(actorID, e.GetEventId(), state.Generation)

		wfLogger.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s'", actorID, targetActorID)
		req := invokev1.
			NewInvokeMethodRequest("Execute").
			WithActor(wf.config.activityActorType, targetActorID).
			WithRawDataBytes(activityRequestBytes).
			WithContentType(invokev1.OctetStreamContentType)
		defer req.Close()

		wf.activityResultAwaited.Store(true)

		resp, err := wf.actors.Call(ctx, req)
		if err != nil {
			if errors.Is(err, ErrDuplicateInvocation) {
				wfLogger.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", actorID, ts.GetName(), e.GetEventId())
				continue
			}
			return newRecoverableError(fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err))
		}
		resp.Close()
	}

	// TODO: Do these in parallel?
	for method, msgList := range reqsByName {
		for _, msg := range msgList {
			eventData, err := backend.MarshalHistoryEvent(msg.HistoryEvent)
			if err != nil {
				return err
			}

			wfLogger.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", actorID, method, msg.TargetInstanceID)
			req := invokev1.
				NewInvokeMethodRequest(method).
				WithActor(wf.config.workflowActorType, msg.TargetInstanceID).
				WithRawDataBytes(eventData).
				WithContentType(invokev1.OctetStreamContentType)
			defer req.Close()

			resp, err := wf.actors.Call(ctx, req)
			if err != nil {
				// workflow-related actor methods are never expected to return errors
				return newRecoverableError(fmt.Errorf("method %s on actor '%s' returned an error: %w", method, msg.TargetInstanceID, err))
			}
			defer resp.Close()
		}
	}

	state.ApplyRuntimeStateChanges(runtimeState)
	state.ClearInbox()

	return wf.saveInternalState(ctx, actorID, state)
}

func (wf *workflowActor) loadInternalState(ctx context.Context, actorID string) (*workflowState, error) {
	if !wf.cachingDisabled {
		// see if the state for this actor is already cached in memory
		cachedState, ok := wf.states.Load(actorID)
		if ok {
			return cachedState.(*workflowState), nil
		}
	}

	// state is not cached, so try to load it from the state store
	wfLogger.Debugf("Workflow actor '%s': loading workflow state", actorID)
	state, err := LoadWorkflowState(ctx, wf.actors, actorID, wf.config)
	if err != nil {
		return nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil
	}
	if !wf.cachingDisabled {
		// update cached state
		wf.states.Store(actorID, state)
	}
	return state, nil
}

func (wf *workflowActor) saveInternalState(ctx context.Context, actorID string, state *workflowState) error {
	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(actorID)
	if err != nil {
		return err
	}

	wfLogger.Debugf("Workflow actor '%s': saving %d keys to actor state store", actorID, len(req.Operations))
	if err = wf.actors.TransactionalStateOperation(ctx, req); err != nil {
		return err
	}

	// ResetChangeTracking should always be called after a save operation succeeds
	state.ResetChangeTracking()

	if !wf.cachingDisabled {
		// update cached state
		wf.states.Store(actorID, state)
	}
	return nil
}

func (wf *workflowActor) createReliableReminder(ctx context.Context, actorID string, namePrefix string, data any, delay time.Duration) (string, error) {
	// Reminders need to have unique names or else they may not fire in certain race conditions.
	reminderName := fmt.Sprintf("%s-%s", namePrefix, uuid.NewString()[:8])
	wfLogger.Debugf("Workflow actor '%s': creating '%s' reminder with DueTime = '%s'", actorID, reminderName, delay)

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

func getRuntimeState(actorID string, state *workflowState) *backend.OrchestrationRuntimeState {
	// TODO: Add caching when a good invalidation policy can be determined
	return backend.NewOrchestrationRuntimeState(api.InstanceID(actorID), state.History)
}

func getActivityActorID(workflowActorID string, taskID int32, generation uint64) string {
	// An activity can be identified by its name followed by its task ID and generation. Example: SayHello::0::1, SayHello::1::1, etc.
	return workflowActorID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}

func (wf *workflowActor) removeCompletedStateData(ctx context.Context, state *workflowState, actorID string) error {
	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	// TODO: for optimization make multiple go routines and run them in parallel
	var err error
	for _, e := range state.Inbox {
		var taskID int32
		if ts := e.GetTaskCompleted(); ts != nil {
			taskID = ts.GetTaskScheduledId()
		} else if tf := e.GetTaskFailed(); tf != nil {
			taskID = tf.GetTaskScheduledId()
		} else {
			continue
		}
		req := actors.TransactionalRequest{
			ActorType: wf.config.activityActorType,
			ActorID:   getActivityActorID(actorID, taskID, state.Generation),
			Operations: []actors.TransactionalOperation{{
				Operation: actors.Delete,
				Request: actors.TransactionalDelete{
					Key: activityStateKey,
				},
			}},
		}
		if err = wf.actors.TransactionalStateOperation(ctx, &req); err != nil {
			return fmt.Errorf("failed to delete activity state with error: %w", err)
		}
	}

	return err
}
