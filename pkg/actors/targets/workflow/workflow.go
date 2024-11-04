/*
Copyright 2024 The Dapr Authors
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
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/dapr/pkg/actors/engine"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/targets"
	"github.com/dapr/dapr/pkg/actors/targets/internal"
	"github.com/dapr/dapr/pkg/components/wfbackend"
	workflowstate "github.com/dapr/dapr/pkg/components/wfbackend/state"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.workflow")

type workflow struct {
	appID             string
	actorID           string
	actorType         string
	activityActorType string

	resiliency resiliency.Provider
	state      state.Interface
	reminders  reminders.Interface
	engine     engine.Interface

	lock             *internal.Lock
	reminderInterval time.Duration

	wState          *workflowstate.State
	cachingDisabled bool

	scheduler             wfbackend.WorkflowScheduler
	activityResultAwaited atomic.Bool
	completed             atomic.Bool
	// TODO: @joshvanl: remove
	defaultTimeout time.Duration
}

type WorkflowOptions struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
	CachingDisabled   bool
	DefaultTimeout    *time.Duration
	ReminderInterval  *time.Duration

	Resiliency  resiliency.Provider
	State       state.Interface
	Reminders   reminders.Interface
	ActorEngine engine.Interface
	Scheduler   wfbackend.WorkflowScheduler
}

func WorkflowFactory(opts WorkflowOptions) targets.Factory {
	return func(actorID string) targets.Interface {
		reminderInterval := time.Minute * 1
		defaultTimeout := time.Second * 30

		if opts.ReminderInterval != nil {
			reminderInterval = *opts.ReminderInterval
		}
		if opts.DefaultTimeout != nil {
			defaultTimeout = *opts.DefaultTimeout
		}

		return &workflow{
			appID:             opts.AppID,
			actorID:           actorID,
			actorType:         opts.WorkflowActorType,
			activityActorType: opts.ActivityActorType,
			scheduler:         opts.Scheduler,
			cachingDisabled:   opts.CachingDisabled,
			reminderInterval:  reminderInterval,
			defaultTimeout:    defaultTimeout,
			resiliency:        opts.Resiliency,
			state:             opts.State,
			reminders:         opts.Reminders,
			engine:            opts.ActorEngine,
			lock:              internal.NewLock(32),
		}
	}
}

// InvokeMethod implements actors.InternalActor
func (w *workflow) InvokeMethod(ctx context.Context, req *internalv1pb.InternalInvokeRequest) (*internalv1pb.InternalInvokeResponse, error) {
	// TODO: @joshvanl: lock actor

	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	if err := w.lock.Lock(imReq); err != nil {
		return nil, err
	}
	defer w.lock.Unlock()

	policyDef := w.resiliency.ActorPostLockPolicy(w.actorType, w.actorID)
	policyRunner := resiliency.NewRunner[*internalv1pb.InternalInvokeResponse](ctx, policyDef)
	msg := imReq.Message()
	return policyRunner(func(ctx context.Context) (*internalv1pb.InternalInvokeResponse, error) {
		resData, err := w.executeMethod(ctx, msg.GetMethod(), msg.GetData().GetValue())
		if err != nil {
			return nil, fmt.Errorf("error from internal actor: %w", err)
		}

		return &internalv1pb.InternalInvokeResponse{
			Status: &internalv1pb.Status{
				Code: http.StatusOK,
			},
			Message: &commonv1pb.InvokeResponse{
				Data: &anypb.Any{
					Value: resData,
				},
			},
		}, nil
	})
}

func (w *workflow) executeMethod(ctx context.Context, methodName string, request []byte) (res []byte, err error) {
	log.Debugf("Workflow actor '%s': invoking method '%s'", w.actorID, methodName)

	switch methodName {
	case wfbackend.CreateWorkflowInstanceMethod:
		err = w.createWorkflowInstance(ctx, request)
	case wfbackend.GetWorkflowMetadataMethod:
		var resAny any
		resAny, err = w.getWorkflowMetadata(ctx)
		if err == nil {
			var resBuffer bytes.Buffer
			err = gob.NewEncoder(&resBuffer).Encode(resAny)
			res = resBuffer.Bytes()
		}
	case wfbackend.GetWorkflowStateMethod:
		var state *workflowstate.State
		state, err = w.getWorkflowState(ctx)
		if err == nil {
			res, err = state.EncodeWorkflowState()
		}
	case wfbackend.AddWorkflowEventMethod:
		err = w.addWorkflowEvent(ctx, request)
	case wfbackend.PurgeWorkflowStateMethod:
		err = w.purgeWorkflowState(ctx)
	default:
		err = fmt.Errorf("no such method: %s", methodName)
	}

	return res, err
}

// InvokeReminder implements actors.InternalActor
func (w *workflow) InvokeReminder(ctx context.Context, reminder *requestresponse.Reminder) error {
	// TODO: @joshvanl: lock actor

	log.Debugf("Workflow actor '%s': invoking reminder '%s'", w.actorID, reminder.Name)

	// Workflow executions should never take longer than a few seconds at the most
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, w.defaultTimeout)
	defer cancelTimeout()
	completed, err := w.runWorkflow(timeoutCtx, reminder)

	if completed == runCompletedTrue {
		w.completed.Store(true)
	}

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		return actorerrors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", w.actorID, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", w.actorID, err)
		return nil
	case wfbackend.IsRecoverableError(err):
		log.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", w.actorID, err)
		return nil
	default: // Other error
		log.Errorf("Workflow actor '%s': execution failed with a non-recoverable error: %v", w.actorID, err)
		return actorerrors.ErrReminderCanceled
	}
}

// InvokeTimer implements actors.InternalActor
func (w *workflow) InvokeTimer(ctx context.Context, reminder *requestresponse.Reminder) error {
	// TODO: @joshvanl: lock actor
	return errors.New("timers are not implemented")
}

// DeactivateActor implements actors.InternalActor
func (w *workflow) DeactivateActor(ctx context.Context) error {
	log.Debugf("Workflow actor '%s': deactivating", w.actorID)
	w.state = nil // A bit of extra caution, shouldn't be necessary
	return nil
}

func (w *workflow) createWorkflowInstance(ctx context.Context, request []byte) error {
	// create a new state entry if one doesn't already exist
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}

	created := false
	if state == nil {
		state = workflowstate.NewState(workflowstate.Options{
			AppID:             w.appID,
			WorkflowActorType: w.actorType,
			ActivityActorType: w.activityActorType,
		})
		created = true
	}

	var createWorkflowInstanceRequest wfbackend.CreateWorkflowInstanceRequest
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
			log.Debugf("Workflow actor '%s': creating workflow '%s' with instanceId '%s'", w.actorID, es.GetName(), es.GetOrchestrationInstance().GetInstanceId())
		} else {
			log.Debugf("Workflow actor '%s': creating child workflow '%s' with instanceId '%s' parentWorkflow '%s' parentWorkflowId '%s'", es.GetName(), es.GetOrchestrationInstance().GetInstanceId(), es.GetParentInstance().GetName(), es.GetParentInstance().GetOrchestrationInstance().GetInstanceId())
		}
	}

	// orchestration didn't exist and was just created
	if created {
		return w.scheduleWorkflowStart(ctx, startEvent, state)
	}

	// orchestration already existed: apply reuse id policy
	runtimeState := getRuntimeState(w.actorID, state)
	runtimeStatus := runtimeState.RuntimeStatus()
	// if target status doesn't match, fall back to original logic, create instance only if previous one is completed
	if !isStatusMatch(reuseIDPolicy.GetOperationStatus(), runtimeStatus) {
		return w.createIfCompleted(ctx, runtimeState, state, startEvent)
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
	return w.createIfCompleted(ctx, runtimeState, state, startEvent)
}

func isStatusMatch(statuses []api.OrchestrationStatus, runtimeStatus api.OrchestrationStatus) bool {
	for _, status := range statuses {
		if status == runtimeStatus {
			return true
		}
	}
	return false
}

func (w *workflow) Completed() bool {
	return w.completed.Load()
}

func (w *workflow) createIfCompleted(ctx context.Context, runtimeState *backend.OrchestrationRuntimeState, state *workflowstate.State, startEvent *backend.HistoryEvent) error {
	// We block (re)creation of existing workflows unless they are in a completed state
	// Or if they still have any pending activity result awaited.
	if !runtimeState.IsCompleted() {
		return fmt.Errorf("an active workflow with ID '%s' already exists", w.actorID)
	}
	if w.activityResultAwaited.Load() {
		return fmt.Errorf("a terminated workflow with ID '%s' is already awaiting an activity result", w.actorID)
	}
	log.Infof("Workflow actor '%s': workflow was previously completed and is being recreated", w.actorID)
	state.Reset()
	return w.scheduleWorkflowStart(ctx, startEvent, state)
}

func (w *workflow) scheduleWorkflowStart(ctx context.Context, startEvent *backend.HistoryEvent, state *workflowstate.State) error {
	// Schedule a reminder to execute immediately after this operation. The reminder will trigger the actual
	// workflow execution. This is preferable to using the current thread so that we don't block the client
	// while the workflow logic is running.
	if _, err := w.createReliableReminder(ctx, "start", nil, 0); err != nil {
		return err
	}

	state.AddToInbox(startEvent)
	return w.saveInternalState(ctx, state)
}

// This method cleans up a workflow associated with the given actorID
func (w *workflow) cleanupWorkflowStateInternal(ctx context.Context, state *workflowstate.State, requiredAndNotCompleted bool) error {
	// If the workflow is required to complete but it's not yet completed then return [ErrNotCompleted]
	// This check is used by purging workflow
	if requiredAndNotCompleted {
		return api.ErrNotCompleted
	}

	err := w.removeCompletedStateData(ctx, state)
	if err != nil {
		return err
	}

	// This will create a request to purge everything
	req, err := state.GetPurgeRequest(w.actorID)
	if err != nil {
		return err
	}
	// This will do the purging
	err = w.state.TransactionalStateOperation(ctx, req)
	if err != nil {
		return err
	}
	w.state = nil
	return nil
}

func (w *workflow) getWorkflowMetadata(ctx context.Context) (*api.OrchestrationMetadata, error) {
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	runtimeState := getRuntimeState(w.actorID, state)

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

func (w *workflow) getWorkflowState(ctx context.Context) (*workflowstate.State, error) {
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, api.ErrInstanceNotFound
	}
	return state, nil
}

// This method purges all the completed activity data from a workflow associated with the given actorID
func (w *workflow) purgeWorkflowState(ctx context.Context) error {
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}
	runtimeState := getRuntimeState(w.actorID, state)
	w.completed.Store(true)
	return w.cleanupWorkflowStateInternal(ctx, state, !runtimeState.IsCompleted())
}

func (w *workflow) addWorkflowEvent(ctx context.Context, historyEventBytes []byte) error {
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}

	e, err := backend.UnmarshalHistoryEvent(historyEventBytes)
	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		w.activityResultAwaited.CompareAndSwap(true, false)
	}
	if err != nil {
		return err
	}
	log.Debugf("Workflow actor '%s': adding event '%v' to the workflow inbox", w.actorID, e)
	state.AddToInbox(e)

	if _, err := w.createReliableReminder(ctx, "new-event", nil, 0); err != nil {
		return err
	}
	return w.saveInternalState(ctx, state)
}

func (w *workflow) getWorkflowName(oldEvents, newEvents []*backend.HistoryEvent) string {
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

func (w *workflow) runWorkflow(ctx context.Context, reminder *requestresponse.Reminder) (runCompleted, error) {
	state, err := w.loadInternalState(ctx)
	if err != nil {
		return runCompletedTrue, fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		return runCompletedTrue, errors.New("no workflow state found")
	}

	if strings.HasPrefix(reminder.Name, "timer-") {
		var timerData wfbackend.DurableTimer
		if err = json.Unmarshal(reminder.Data, &timerData); err != nil {
			// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
			return runCompletedTrue, err
		}
		if timerData.Generation < state.Generation {
			log.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", w.actorID, timerData.Generation)
			return runCompletedFalse, nil
		} else {
			e, eventErr := backend.UnmarshalHistoryEvent(timerData.Bytes)
			if eventErr != nil {
				// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
				return runCompletedTrue, fmt.Errorf("failed to unmarshal timer data %w", eventErr)
			}
			state.Inbox = append(state.Inbox, e)
		}
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		log.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", w.actorID, reminder.Name)
		return runCompletedFalse, nil
	}

	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	transactionalRequests := make(map[string][]requestresponse.TransactionalOperation)
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
		op := requestresponse.TransactionalOperation{
			Operation: requestresponse.Delete,
			Request: requestresponse.TransactionalDelete{
				Key: activityStateKey,
			},
		}
		activityActorID := getActivityActorID(w.actorID, taskID, state.Generation)
		if transactionalRequests[activityActorID] == nil {
			transactionalRequests[activityActorID] = []requestresponse.TransactionalOperation{op}
		} else {
			transactionalRequests[activityActorID] = append(transactionalRequests[activityActorID], op)
		}
	}
	// TODO: for optimization make multiple go routines and run them in parallel
	for activityActorID, operations := range transactionalRequests {
		err = w.state.TransactionalStateOperation(ctx, &requestresponse.TransactionalRequest{
			ActorType:  w.activityActorType,
			ActorID:    activityActorID,
			Operations: operations,
		})
		if err != nil {
			return runCompletedFalse, fmt.Errorf("failed to delete activity state for activity actor '%s' with error: %w", activityActorID, err)
		}
	}

	runtimeState := getRuntimeState(w.actorID, state)
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
	wi.Properties[wfbackend.CallbackChannelProperty] = callback
	// Setting executionStatus to failed by default to record metrics for non-recoverable errors.
	executionStatus := diag.StatusFailed
	if runtimeState.IsCompleted() {
		// If workflow is already completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
	}
	workflowName := w.getWorkflowName(state.History, state.Inbox)
	// Request to execute workflow
	log.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", w.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	// TODO: @joshvanl
	err = w.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("failed to schedule a workflow execution: %w", err))
	}

	w.recordWorkflowSchedulingLatency(ctx, esHistoryEvent, workflowName)
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
		return runCompletedFalse, ctx.Err()
	case completed := <-callback:
		if !completed {
			// Workflow execution failed with recoverable error
			executionStatus = diag.StatusRecoverable
			return runCompletedFalse, wfbackend.NewRecoverableError(errExecutionAborted)
		}
	}
	log.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", w.actorID, runtimeState.RuntimeStatus().String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if runtimeState.ContinuedAsNew() {
		log.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", w.actorID, wi.InstanceID)
		state.Generation += 1
	}

	if !runtimeState.IsCompleted() {
		// Create reminders for the durable timers. We only do this if the orchestration is still running.
		for _, t := range runtimeState.PendingTimers() {
			tf := t.GetTimerFired()
			if tf == nil {
				return runCompletedTrue, errors.New("invalid event in the PendingTimers list")
			}
			timerBytes, errMarshal := backend.MarshalHistoryEvent(t)
			if errMarshal != nil {
				return runCompletedTrue, fmt.Errorf("failed to marshal pending timer data: %w", errMarshal)
			}
			delay := time.Until(tf.GetFireAt().AsTime())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := "timer-" + strconv.Itoa(int(tf.GetTimerId()))
			data := wfbackend.NewDurableTimer(timerBytes, state.Generation)
			log.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer", w.actorID, reminderPrefix)
			if _, err = w.createReliableReminder(ctx, reminderPrefix, data, delay); err != nil {
				executionStatus = diag.StatusRecoverable
				return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("actor '%s' failed to create reminder for timer: %w", w.actorID, err))
			}
		}
	}

	// Process the outbound orchestrator events
	reqsByName := make(map[string][]backend.OrchestratorMessage, len(runtimeState.PendingMessages()))
	for _, msg := range runtimeState.PendingMessages() {
		if es := msg.HistoryEvent.GetExecutionStarted(); es != nil {
			reqsByName[wfbackend.CreateWorkflowInstanceMethod] = append(reqsByName[wfbackend.CreateWorkflowInstanceMethod], msg)
		} else if msg.HistoryEvent.GetSubOrchestrationInstanceCompleted() != nil || msg.HistoryEvent.GetSubOrchestrationInstanceFailed() != nil {
			reqsByName[wfbackend.AddWorkflowEventMethod] = append(reqsByName[wfbackend.AddWorkflowEventMethod], msg)
		} else {
			log.Warnf("Workflow actor '%s': don't know how to process outbound message '%v'", w.actorID, msg)
		}
	}

	// Schedule activities
	// TODO: Parallelism
	for _, e := range runtimeState.PendingTasks() {
		ts := e.GetTaskScheduled()
		if ts == nil {
			log.Warnf("Workflow actor '%s': unable to process task '%v'", w.actorID, e)
			continue
		}

		eventData, errMarshal := backend.MarshalHistoryEvent(e)
		if errMarshal != nil {
			return runCompletedTrue, errMarshal
		}

		var res bytes.Buffer
		if err := gob.NewEncoder(&res).Encode(&ActivityRequest{
			HistoryEvent: eventData,
		}); err != nil {
			return runCompletedTrue, err
		}
		targetActorID := getActivityActorID(w.actorID, e.GetEventId(), state.Generation)

		w.activityResultAwaited.Store(true)

		log.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s'", w.actorID, targetActorID)

		_, err = w.engine.Call(ctx, internalsv1pb.
			NewInternalInvokeRequest("Execute").
			WithActor(w.activityActorType, targetActorID).
			WithData(res.Bytes()).
			WithContentType(invokev1.OctetStreamContentType),
		)

		if errors.Is(err, ErrDuplicateInvocation) {
			log.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", w.actorID, ts.GetName(), e.GetEventId())
			continue
		} else if err != nil {
			executionStatus = diag.StatusRecoverable
			return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), err))
		}
	}

	// TODO: Do these in parallel?
	for method, msgList := range reqsByName {
		for _, msg := range msgList {
			eventData, errMarshal := backend.MarshalHistoryEvent(msg.HistoryEvent)
			if errMarshal != nil {
				return runCompletedTrue, errMarshal
			}

			requestBytes := eventData
			if method == wfbackend.CreateWorkflowInstanceMethod {
				requestBytes, err = json.Marshal(wfbackend.CreateWorkflowInstanceRequest{
					Policy:          &api.OrchestrationIdReusePolicy{},
					StartEventBytes: eventData,
				})
				if err != nil {
					return runCompletedTrue, fmt.Errorf("failed to marshal createWorkflowInstanceRequest: %w", err)
				}
			}

			log.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", w.actorID, method, msg.TargetInstanceID)

			_, err = w.engine.Call(ctx, internalsv1pb.
				NewInternalInvokeRequest(method).
				WithActor(w.actorType, msg.TargetInstanceID).
				WithData(requestBytes).
				WithContentType(invokev1.OctetStreamContentType),
			)

			//_, err = w.actors.Call(ctx, req)
			if err != nil {
				executionStatus = diag.StatusRecoverable
				// workflow-related actor methods are never expected to return errors
				return runCompletedFalse, wfbackend.NewRecoverableError(fmt.Errorf("method %s on actor '%s' returned an error: %w", method, msg.TargetInstanceID, err))
			}
		}
	}

	state.ApplyRuntimeStateChanges(runtimeState)
	state.ClearInbox()

	err = w.saveInternalState(ctx, state)
	if err != nil {
		return runCompletedTrue, err
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
			wfExecutionElapsedTime = w.calculateWorkflowExecutionLatency(state)
		}
	}
	if runtimeState.IsCompleted() {
		log.Infof("Workflow Actor '%s': workflow completed with status '%s' workflowName '%s'", w.actorID, runtimeState.RuntimeStatus().String(), workflowName)
		return runCompletedTrue, nil
	}
	return runCompletedFalse, nil
}

func (*workflow) calculateWorkflowExecutionLatency(state *workflowstate.State) (wExecutionElapsedTime float64) {
	for _, e := range state.History {
		if os := e.GetOrchestratorStarted(); os != nil {
			return diag.ElapsedSince(e.GetTimestamp().AsTime())
		}
	}
	return 0
}

func (*workflow) recordWorkflowSchedulingLatency(ctx context.Context, esHistoryEvent *backend.HistoryEvent, workflowName string) {
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

func (w *workflow) loadInternalState(ctx context.Context) (*workflowstate.State, error) {
	// See if the state for this actor is already cached in memory
	if !w.cachingDisabled && w.state != nil {
		return w.wState, nil
	}

	// state is not cached, so try to load it from the state store
	log.Debugf("Workflow actor '%s': loading workflow state", w.actorID)
	state, err := workflowstate.LoadWorkflowState(ctx, w.state, w.actorID, workflowstate.Options{
		AppID:             w.appID,
		WorkflowActorType: w.actorType,
		ActivityActorType: w.activityActorType,
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil
	}
	if !w.cachingDisabled {
		// Update cached state
		w.wState = state
	}
	return state, nil
}

func (w *workflow) saveInternalState(ctx context.Context, state *workflowstate.State) error {
	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(w.actorID)
	if err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': saving %d keys to actor state store", w.actorID, len(req.Operations))
	if err = w.state.TransactionalStateOperation(ctx, req); err != nil {
		return err
	}

	// ResetChangeTracking should always be called after a save operation succeeds
	state.ResetChangeTracking()

	if !w.cachingDisabled {
		// Update cached state
		w.wState = state
	}
	return nil
}

func (w *workflow) createReliableReminder(ctx context.Context, namePrefix string, data any, delay time.Duration) (string, error) {
	// Reminders need to have unique names or else they may not fire in certain race conditions.
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}
	reminderName := namePrefix + "-" + base64.RawURLEncoding.EncodeToString(b)
	log.Debugf("Workflow actor '%s': creating '%s' reminder with DueTime = '%s'", w.actorID, reminderName, delay)

	dataEnc, err := json.Marshal(data)
	if err != nil {
		return reminderName, fmt.Errorf("failed to encode data as JSON: %w", err)
	}

	return reminderName, w.reminders.Create(ctx, &requestresponse.CreateReminderRequest{
		ActorType: w.actorType,
		ActorID:   w.actorID,
		Data:      dataEnc,
		DueTime:   delay.String(),
		Name:      reminderName,
		// TODO: @joshvanl: remove when using scheduler to make once shot.
		Period: w.reminderInterval.String(),
	})
}

func getRuntimeState(actorID string, state *workflowstate.State) *backend.OrchestrationRuntimeState {
	// TODO: Add caching when a good invalidation policy can be determined
	return backend.NewOrchestrationRuntimeState(api.InstanceID(actorID), state.History)
}

func getActivityActorID(workflowID string, taskID int32, generation uint64) string {
	// An activity can be identified by its name followed by its task ID and generation. Example: SayHello::0::1, SayHello::1::1, etc.
	return workflowID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}

func (w *workflow) removeCompletedStateData(ctx context.Context, state *workflowstate.State) error {
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
		req := requestresponse.TransactionalRequest{
			ActorType: w.activityActorType,
			ActorID:   getActivityActorID(w.actorID, taskID, state.Generation),
			Operations: []requestresponse.TransactionalOperation{{
				Operation: requestresponse.Delete,
				Request: requestresponse.TransactionalDelete{
					Key: activityStateKey,
				},
			}},
		}
		if err = w.state.TransactionalStateOperation(ctx, &req); err != nil {
			return fmt.Errorf("failed to delete activity state with error: %w", err)
		}
	}

	return err
}

// DeactivateActor implements actors.InternalActor
func (w *workflow) Deactivate(ctx context.Context) error {
	log.Debugf("Workflow actor '%s': deactivating", w.actorID)
	w.state = nil // A bit of extra caution, shouldn't be necessary
	return nil
}
