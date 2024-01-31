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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"

	"github.com/dapr/dapr/pkg/actors"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

const (
	CallbackChannelProperty = "dapr.callback"

	CreateWorkflowInstanceMethod = "CreateWorkflowInstance"
	GetWorkflowMetadataMethod    = "GetWorkflowMetadata"
	AddWorkflowEventMethod       = "AddWorkflowEvent"
	PurgeWorkflowStateMethod     = "PurgeWorkflowState"
	GetWorkflowStateMethod       = "GetWorkflowState"
)

type workflowActor struct {
	actorID               string
	actors                actors.Actors
	state                 *workflowState
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

type workflowActorOpts struct {
	cachingDisabled  bool
	defaultTimeout   time.Duration
	reminderInterval time.Duration
}

func NewWorkflowActor(scheduler workflowScheduler, config actorsBackendConfig, opts *workflowActorOpts) actors.InternalActorFactory {
	return func(actorType string, actorID string, actors actors.Actors) actors.InternalActor {
		wf := &workflowActor{
			actorID:          actorID,
			actors:           actors,
			scheduler:        scheduler,
			defaultTimeout:   30 * time.Second,
			reminderInterval: 1 * time.Minute,
			config:           config,
			cachingDisabled:  opts.cachingDisabled,
		}

		if opts.defaultTimeout > 0 {
			wf.defaultTimeout = opts.defaultTimeout
		}
		if opts.reminderInterval > 0 {
			wf.reminderInterval = opts.reminderInterval
		}

		return wf
	}
}

// InvokeMethod implements actors.InternalActor
func (wf *workflowActor) InvokeMethod(ctx context.Context, methodName string, request []byte, metadata map[string][]string) (res []byte, err error) {
	wfLogger.Debugf("Workflow actor '%s': invoking method '%s'", wf.actorID, methodName)

	switch methodName {
	case CreateWorkflowInstanceMethod:
		err = wf.createWorkflowInstance(ctx, request)
	case GetWorkflowMetadataMethod:
		var resAny any
		resAny, err = wf.getWorkflowMetadata(ctx)
		if err == nil {
			res, err = actors.EncodeInternalActorData(resAny)
		}
	case GetWorkflowStateMethod:
		var state *workflowState
		state, err = wf.getWorkflowState(ctx)
		if err == nil {
			res, err = state.EncodeWorkflowState()
		}
	case AddWorkflowEventMethod:
		err = wf.addWorkflowEvent(ctx, request)
	case PurgeWorkflowStateMethod:
		err = wf.purgeWorkflowState(ctx)
	default:
		err = fmt.Errorf("no such method: %s", methodName)
	}

	return res, err
}

// InvokeReminder implements actors.InternalActor
func (wf *workflowActor) InvokeReminder(ctx context.Context, reminder actors.InternalActorReminder, metadata map[string][]string) error {
	wfLogger.Debugf("Workflow actor '%s': invoking reminder '%s'", wf.actorID, reminder.Name)

	// Workflow executions should never take longer than a few seconds at the most
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, wf.defaultTimeout)
	defer cancelTimeout()
	err := wf.runWorkflow(timeoutCtx, reminder)

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	var re recoverableError
	switch {
	case err == nil:
		return actors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		wfLogger.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", wf.actorID, err)
		return nil
	case errors.Is(err, context.Canceled):
		wfLogger.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", wf.actorID, err)
		return nil
	case errors.As(err, &re):
		wfLogger.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", wf.actorID, re)
		return nil
	default: // Other error
		wfLogger.Errorf("Workflow actor '%s': execution failed with a non-recoverable error: %v", wf.actorID, err)
		return actors.ErrReminderCanceled
	}
}

// InvokeTimer implements actors.InternalActor
func (wf *workflowActor) InvokeTimer(ctx context.Context, timer actors.InternalActorReminder, metadata map[string][]string) error {
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (wf *workflowActor) DeactivateActor(ctx context.Context) error {
	wfLogger.Debugf("Workflow actor '%s': deactivating", wf.actorID)
	wf.state = nil // A bit of extra caution, shouldn't be necessary
	return nil
}

func (wf *workflowActor) createWorkflowInstance(ctx context.Context, request []byte) error {
	// create a new state entry if one doesn't already exist
	state, err := wf.loadInternalState(ctx)
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
			wfLogger.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'", wf.actorID, es.GetName(), es.GetOrchestrationInstance().GetInstanceId())
		} else {
			wfLogger.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'", es.GetName(), es.GetOrchestrationInstance().GetInstanceId(), es.GetParentInstance().GetName(), es.GetParentInstance().GetOrchestrationInstance().GetInstanceId())
		}
	}

	// orchestration didn't exist and was just created
	if created {
		return wf.scheduleWorkflowStart(ctx, startEvent, state)
	}

	// orchestration already existed: apply reuse id policy
	runtimeState := getRuntimeState(wf.actorID, state)
	runtimeStatus := runtimeState.RuntimeStatus()
	// if target status doesn't match, fall back to original logic, create instance only if previous one is completed
	if !isStatusMatch(reuseIDPolicy.GetOperationStatus(), runtimeStatus) {
		return wf.createIfCompleted(ctx, runtimeState, state, startEvent)
	}

	switch reuseIDPolicy.GetAction() {
	case api.REUSE_ID_ACTION_IGNORE:
		// Log an warning message and ignore creating new instance
		wfLogger.Warnf("Workflow actor '%s': ignoring request to recreate the current workflow instance", wf.actorID)
		return nil
	case api.REUSE_ID_ACTION_TERMINATE:
		// terminate existing instance
		if err := wf.cleanupWorkflowStateInternal(ctx, state, false); err != nil {
			return fmt.Errorf("failed to terminate existing instance with ID '%s'", wf.actorID)
		}

		// created a new instance
		state.Reset()
		return wf.scheduleWorkflowStart(ctx, startEvent, state)
	}
	// default Action ERROR, fall back to original logic
	return wf.createIfCompleted(ctx, runtimeState, state, startEvent)
}

func isStatusMatch(statuses []api.OrchestrationStatus, runtimeStatus api.OrchestrationStatus) bool {
	for _, status := range statuses {
		if status == runtimeStatus {
			return true
		}
	}
	return false
}

func (wf *workflowActor) createIfCompleted(ctx context.Context, runtimeState *backend.OrchestrationRuntimeState, state *workflowState, startEvent *backend.HistoryEvent) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimeState.IsCompleted() {
		return fmt.Errorf("an active workflow with ID '%s' already exists", wf.actorID)
	}
	if wf.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", wf.actorID)
	}
	wfLogger.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", wf.actorID)
	state.Reset()
	return wf.scheduleWorkflowStart(ctx, startEvent, state)
}

func (wf *workflowActor) scheduleWorkflowStart(ctx context.Context, startEvent *backend.HistoryEvent, state *workflowState) error {
	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := wf.createReliableReminder(ctx, "start", nil, 0); err != nil {
		return err
	}

	state.AddToInbox(startEvent)
	return wf.saveInternalState(ctx, state)
}

// This method cleans up a workflow associated with the given actorID
func (wf *workflowActor) cleanupWorkflowStateInternal(ctx context.Context, state *workflowState, requiredAndNotCompleted bool) error {
	// If the workflow is required to complete but it's not yet completed then return [ErrNotCompleted]
	// This check is used by purging workflow
	if requiredAndNotCompleted {
		return api.ErrNotCompleted
	}

	err := wf.removeCompletedStateData(ctx, state)
	if err != nil {
		return err
	}

	// This will create a request to purge everything
	req, err := state.GetPurgeRequest(wf.actorID)
	if err != nil {
		return err
	}
	// This will do the purging
	err = wf.actors.TransactionalStateOperation(ctx, req)
	if err != nil {
		return err
	}
	wf.state = nil
	return nil
}

func (wf *workflowActor) getWorkflowMetadata(ctx context.Context) (*api.OrchestrationMetadata, error) {
	state, err := wf.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	runtimeState := getRuntimeState(wf.actorID, state)

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

func (wf *workflowActor) getWorkflowState(ctx context.Context) (*workflowState, error) {
	state, err := wf.loadInternalState(ctx)
	wfLogger.Errorf("Workflow actor '%s': getWorkflowState, state: %s", wf.actorID, state)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}
	return state, nil
}

// This method purges all the completed activity data from a workflow associated with the given actorID
func (wf *workflowActor) purgeWorkflowState(ctx context.Context) error {
	state, err := wf.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}
	runtimeState := getRuntimeState(wf.actorID, state)
	return wf.cleanupWorkflowStateInternal(ctx, state, !runtimeState.IsCompleted())
}

func (wf *workflowActor) addWorkflowEvent(ctx context.Context, historyEventBytes []byte) error {
	state, err := wf.loadInternalState(ctx)
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
	wfLogger.Debugf("Workflow actor '%s': adding event '%v' to the workflow inbox", wf.actorID, e)
	state.AddToInbox(e)

	if _, err := wf.createReliableReminder(ctx, "new-event", nil, 0); err != nil {
		return err
	}
	return wf.saveInternalState(ctx, state)
}

func (wf *workflowActor) getWorkflowName(oldEvents, newEvents []*backend.HistoryEvent) string {
	for _, e := range oldEvents {
		if es := e.GetExecutionStarted(); es != nil {
			return es.GetName()
		}
	}
	for _, e := range newEvents {
		if es := e.GetExecutionStarted(); es != nil {
			return es.GetName()
		}
	}
	return ""
}

func (wf *workflowActor) runWorkflow(ctx context.Context, reminder actors.InternalActorReminder) error {
	state, err := wf.loadInternalState(ctx)
	if err != nil {
		return fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		return errors.New("no workflow state found")
	}

	if strings.HasPrefix(reminder.Name, "timer-") {
		var timerData durableTimer
		if err = reminder.DecodeData(&timerData); err != nil {
			// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
			return err
		}
		if timerData.Generation < state.Generation {
			wfLogger.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", wf.actorID, timerData.Generation)
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
		wfLogger.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", wf.actorID, reminder.Name)
		return nil
	}

	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	transactionalRequests := make(map[string][]actors.TransactionalOperation)
	var esHistoryEvent *backend.HistoryEvent

	for _, e := range state.Inbox {
		var taskID int32
		if ts := e.GetTaskCompleted(); ts != nil {
			taskID = ts.GetTaskScheduledId()
		} else if tf := e.GetTaskFailed(); tf != nil {
			taskID = tf.GetTaskScheduledId()
		} else {
			if es := e.GetExecutionStarted(); es != nil {
				esHistoryEvent = e
			}
			continue
		}
		op := actors.TransactionalOperation{
			Operation: actors.Delete,
			Request: actors.TransactionalDelete{
				Key: activityStateKey,
			},
		}
		activityActorID := getActivityActorID(wf.actorID, taskID, state.Generation)
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

	runtimeState := getRuntimeState(wf.actorID, state)
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
	// Setting executionStatus to failed by default to record metrics for non-recoverable errors.
	executionStatus := diag.StatusFailed
	if runtimeState.IsCompleted() {
		// If workflow is already completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
	}
	workflowName := wf.getWorkflowName(state.History, state.Inbox)
	// Request to execute workflow
	wfLogger.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", wf.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	err = wf.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return newRecoverableError(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return newRecoverableError(fmt.Errorf("failed to schedule a workflow execution: %w", err))
	}

	wf.recordWorkflowSchedulingLatency(ctx, esHistoryEvent, workflowName)
	wfExecutionElapsedTime := float64(0)

	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.WorkflowExecutionEvent(ctx, workflowName, executionStatus)
			diag.DefaultWorkflowMonitoring.WorkflowExecutionLatency(ctx, workflowName, executionStatus, wfExecutionElapsedTime)
		}
	}()

	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		// Workflow execution failed with recoverable error
		executionStatus = diag.StatusRecoverable
		return ctx.Err()
	case completed := <-callback:
		if !completed {
			// Workflow execution failed with recoverable error
			executionStatus = diag.StatusRecoverable
			return newRecoverableError(errExecutionAborted)
		}
	}
	wfLogger.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", wf.actorID, runtimeState.RuntimeStatus().String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if runtimeState.ContinuedAsNew() {
		wfLogger.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", wf.actorID, wi.InstanceID)
		state.Generation += 1
	}

	if !runtimeState.IsCompleted() {
		// Create reminders for the durable timers. We only do this if the orchestration is still running.
		for _, t := range runtimeState.PendingTimers() {
			tf := t.GetTimerFired()
			if tf == nil {
				return errors.New("invalid event in the PendingTimers list")
			}
			timerBytes, errMarshal := backend.MarshalHistoryEvent(t)
			if errMarshal != nil {
				return fmt.Errorf("failed to marshal pending timer data: %w", errMarshal)
			}
			delay := time.Until(tf.GetFireAt().AsTime())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := "timer-" + strconv.Itoa(int(tf.GetTimerId()))
			data := NewDurableTimer(timerBytes, state.Generation)
			wfLogger.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer", wf.actorID, reminderPrefix)
			if _, err = wf.createReliableReminder(ctx, reminderPrefix, data, delay); err != nil {
				executionStatus = diag.StatusRecoverable
				return newRecoverableError(fmt.Errorf("actor '%s' failed to create reminder for timer: %w", wf.actorID, err))
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
			wfLogger.Warnf("Workflow actor '%s': don't know how to process outbound message '%v'", wf.actorID, msg)
		}
	}

	// Schedule activities
	// TODO: Parallelism
	for _, e := range runtimeState.PendingTasks() {
		ts := e.GetTaskScheduled()
		if ts == nil {
			wfLogger.Warnf("Workflow actor '%s': unable to process task '%v'", wf.actorID, e)
			continue
		}

		eventData, errMarshal := backend.MarshalHistoryEvent(e)
		if errMarshal != nil {
			return errMarshal
		}
		activityRequestBytes, errInternal := actors.EncodeInternalActorData(ActivityRequest{
			HistoryEvent: eventData,
		})
		if errInternal != nil {
			return errInternal
		}
		targetActorID := getActivityActorID(wf.actorID, e.GetEventId(), state.Generation)

		wf.activityResultAwaited.Store(true)

		wfLogger.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s'", wf.actorID, targetActorID)
		req := internalsv1pb.
			NewInternalInvokeRequest("Execute").
			WithActor(wf.config.activityActorType, targetActorID).
			WithData(activityRequestBytes).
			WithContentType(invokev1.OctetStreamContentType)

		_, err = wf.actors.Call(ctx, req)

		if errors.Is(err, ErrDuplicateInvocation) {
			wfLogger.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", wf.actorID, ts.GetName(), e.GetEventId())
			continue
		} else if err != nil {
			executionStatus = diag.StatusRecoverable
			return newRecoverableError(fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err))
		}
	}

	// TODO: Do these in parallel?
	for method, msgList := range reqsByName {
		for _, msg := range msgList {
			eventData, errMarshal := backend.MarshalHistoryEvent(msg.HistoryEvent)
			if errMarshal != nil {
				return errMarshal
			}

			requestBytes := eventData
			if method == CreateWorkflowInstanceMethod {
				requestBytes, err = json.Marshal(CreateWorkflowInstanceRequest{
					Policy:          &api.OrchestrationIdReusePolicy{},
					StartEventBytes: eventData,
				})
				if err != nil {
					return fmt.Errorf("failed to marshal createWorkflowInstanceRequest: %w", err)
				}
			}

			wfLogger.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", wf.actorID, method, msg.TargetInstanceID)
			req := internalsv1pb.
				NewInternalInvokeRequest(method).
				WithActor(wf.config.workflowActorType, msg.TargetInstanceID).
				WithData(requestBytes).
				WithContentType(invokev1.OctetStreamContentType)

			_, err = wf.actors.Call(ctx, req)
			if err != nil {
				executionStatus = diag.StatusRecoverable
				// workflow-related actor methods are never expected to return errors
				return newRecoverableError(fmt.Errorf("method %s on actor '%s' returned an error: %w", method, msg.TargetInstanceID, err))
			}
		}
	}

	state.ApplyRuntimeStateChanges(runtimeState)
	state.ClearInbox()

	err = wf.saveInternalState(ctx, state)
	if err != nil {
		return err
	}
	if executionStatus != "" {
		// If workflow is not completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
		if runtimeState.IsCompleted() {
			if runtimeState.RuntimeStatus() == api.RUNTIME_STATUS_COMPLETED {
				executionStatus = diag.StatusSuccess
			} else {
				// Setting executionStatus to failed if workflow has failed/terminated/cancelled
				executionStatus = diag.StatusFailed
			}
			wfExecutionElapsedTime = wf.calculateWorkflowExecutionLatency(state)
		}
	}
	if runtimeState.IsCompleted() {
		wfLogger.Infof("Workflow Actor '%s': workflow completed with status '%s' workflowName '%s'", wf.actorID, runtimeState.RuntimeStatus().String(), workflowName)
	}
	return nil
}

func (*workflowActor) calculateWorkflowExecutionLatency(state *workflowState) (wfExecutionElapsedTime float64) {
	for _, e := range state.History {
		if os := e.GetOrchestratorStarted(); os != nil {
			return diag.ElapsedSince(e.GetTimestamp().AsTime())
		}
	}
	return 0
}

func (*workflowActor) recordWorkflowSchedulingLatency(ctx context.Context, esHistoryEvent *backend.HistoryEvent, workflowName string) {
	if esHistoryEvent == nil {
		return
	}

	// If the event is an execution started event, then we need to record the scheduled start timestamp
	if es := esHistoryEvent.GetExecutionStarted(); es != nil {
		currentTimestamp := time.Now()
		var scheduledStartTimestamp time.Time
		timestamp := es.GetScheduledStartTimestamp()

		if timestamp != nil {
			scheduledStartTimestamp = timestamp.AsTime()
		} else {
			// if scheduledStartTimestamp is nil, then use the event timestamp to consider scheduling latency
			// This case will happen when the workflow is created and started immediately
			scheduledStartTimestamp = esHistoryEvent.GetTimestamp().AsTime()
		}

		wfSchedulingLatency := float64(currentTimestamp.Sub(scheduledStartTimestamp).Milliseconds())
		diag.DefaultWorkflowMonitoring.WorkflowSchedulingLatency(ctx, workflowName, wfSchedulingLatency)
	}
}

func (wf *workflowActor) loadInternalState(ctx context.Context) (*workflowState, error) {
	// See if the state for this actor is already cached in memory
	if !wf.cachingDisabled && wf.state != nil {
		return wf.state, nil
	}

	// state is not cached, so try to load it from the state store
	wfLogger.Debugf("Workflow actor '%s': loading workflow state", wf.actorID)
	state, err := LoadWorkflowState(ctx, wf.actors, wf.actorID, wf.config)
	if err != nil {
		return nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil
	}
	if !wf.cachingDisabled {
		// Update cached state
		wf.state = state
	}
	return state, nil
}

func (wf *workflowActor) saveInternalState(ctx context.Context, state *workflowState) error {
	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(wf.actorID)
	if err != nil {
		return err
	}

	wfLogger.Debugf("Workflow actor '%s': saving %d keys to actor state store", wf.actorID, len(req.Operations))
	if err = wf.actors.TransactionalStateOperation(ctx, req); err != nil {
		return err
	}

	// ResetChangeTracking should always be called after a save operation succeeds
	state.ResetChangeTracking()

	if !wf.cachingDisabled {
		// Update cached state
		wf.state = state
	}
	return nil
}

func (wf *workflowActor) createReliableReminder(ctx context.Context, namePrefix string, data any, delay time.Duration) (string, error) {
	// Reminders need to have unique names or else they may not fire in certain race conditions.
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}
	reminderName := namePrefix + "-" + base64.RawURLEncoding.EncodeToString(b)
	wfLogger.Debugf("Workflow actor '%s': creating '%s' reminder with DueTime = '%s'", wf.actorID, reminderName, delay)

	dataEnc, err := json.Marshal(data)
	if err != nil {
		return reminderName, fmt.Errorf("failed to encode data as JSON: %w", err)
	}

	return reminderName, wf.actors.CreateReminder(ctx, &actors.CreateReminderRequest{
		ActorType: wf.config.workflowActorType,
		ActorID:   wf.actorID,
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

func (wf *workflowActor) removeCompletedStateData(ctx context.Context, state *workflowState) error {
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
			ActorID:   getActivityActorID(wf.actorID, taskID, state.Generation),
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
