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
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors"
	actorapi "github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/engine"
	actorerrors "github.com/dapr/dapr/pkg/actors/errors"
	"github.com/dapr/dapr/pkg/actors/reminders"
	"github.com/dapr/dapr/pkg/actors/state"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/events/broadcaster"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.targets.workflow")

type EventSink func(*backend.OrchestrationMetadata)

type workflow struct {
	appID             string
	actorID           string
	actorType         string
	activityActorType string

	resiliency resiliency.Provider
	engine     engine.Interface
	table      table.Interface
	reminders  reminders.Interface
	actorState state.Interface

	reminderInterval time.Duration

	state            *wfenginestate.State
	rstate           *backend.OrchestrationRuntimeState
	ometa            *backend.OrchestrationMetadata
	ometaBroadcaster *broadcaster.Broadcaster[*backend.OrchestrationMetadata]

	scheduler             todo.WorkflowScheduler
	activityResultAwaited atomic.Bool
	completed             atomic.Bool
	schedulerReminders    bool
	lock                  sync.Mutex
	closeCh               chan struct{}
	closed                atomic.Bool
}

type WorkflowOptions struct {
	AppID             string
	WorkflowActorType string
	ActivityActorType string
	ReminderInterval  *time.Duration

	Resiliency         resiliency.Provider
	Actors             actors.Interface
	Scheduler          todo.WorkflowScheduler
	SchedulerReminders bool
	EventSink          EventSink
}

func WorkflowFactory(ctx context.Context, opts WorkflowOptions) (targets.Factory, error) {
	table, err := opts.Actors.Table(ctx)
	if err != nil {
		return nil, err
	}

	astate, err := opts.Actors.State(ctx)
	if err != nil {
		return nil, err
	}

	engine, err := opts.Actors.Engine(ctx)
	if err != nil {
		return nil, err
	}

	reminders, err := opts.Actors.Reminders(ctx)
	if err != nil {
		return nil, err
	}

	return func(actorID string) targets.Interface {
		reminderInterval := time.Minute * 1

		if opts.ReminderInterval != nil {
			reminderInterval = *opts.ReminderInterval
		}

		w := &workflow{
			appID:              opts.AppID,
			actorID:            actorID,
			actorType:          opts.WorkflowActorType,
			activityActorType:  opts.ActivityActorType,
			scheduler:          opts.Scheduler,
			reminderInterval:   reminderInterval,
			resiliency:         opts.Resiliency,
			table:              table,
			reminders:          reminders,
			engine:             engine,
			actorState:         astate,
			schedulerReminders: opts.SchedulerReminders,
			ometaBroadcaster:   broadcaster.New[*backend.OrchestrationMetadata](),
			closeCh:            make(chan struct{}),
		}
		if opts.EventSink != nil {
			ch := make(chan *backend.OrchestrationMetadata)
			go w.runEventSink(ch, opts.EventSink)
			// We use a Background context since this subscription should be maintained for the entire lifecycle of this workflow actor. The subscription will be shutdown during the actor deactivation.
			w.ometaBroadcaster.Subscribe(context.Background(), ch)
		}
		return w
	}, nil
}

func (w *workflow) runEventSink(ch chan *backend.OrchestrationMetadata, cb func(*backend.OrchestrationMetadata)) {
	for {
		select {
		case <-w.closeCh:
			return
		case val, ok := <-ch:
			if !ok {
				return
			}
			cb(val)
		}
	}
}

// InvokeMethod implements actors.InternalActor
func (w *workflow) InvokeMethod(ctx context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
	w.table.RemoveIdler(w)

	if req.GetMessage() == nil {
		return nil, errors.New("message is nil in request")
	}

	// Create the InvokeMethodRequest
	imReq, err := invokev1.FromInternalInvokeRequest(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create InvokeMethodRequest: %w", err)
	}
	defer imReq.Close()

	policyDef := w.resiliency.ActorPostLockPolicy(w.actorType, w.actorID)
	policyRunner := resiliency.NewRunner[*internalsv1pb.InternalInvokeResponse](ctx, policyDef)
	msg := imReq.Message()
	return policyRunner(func(ctx context.Context) (*internalsv1pb.InternalInvokeResponse, error) {
		resData, err := w.executeMethod(ctx, msg.GetMethod(), msg.GetData().GetValue())
		if err != nil {
			return nil, fmt.Errorf("error from worfklow actor: %w", err)
		}

		return &internalsv1pb.InternalInvokeResponse{
			Status: &internalsv1pb.Status{
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

func (w *workflow) executeMethod(ctx context.Context, methodName string, request []byte) ([]byte, error) {
	log.Debugf("Workflow actor '%s': invoking method '%s'", w.actorID, methodName)

	switch methodName {
	case todo.CreateWorkflowInstanceMethod:
		return nil, w.createWorkflowInstance(ctx, request)

	case todo.AddWorkflowEventMethod:
		return nil, w.addWorkflowEvent(ctx, request)

	case todo.PurgeWorkflowStateMethod:
		return nil, w.purgeWorkflowState(ctx)

	default:
		return nil, fmt.Errorf("no such method: %s", methodName)
	}
}

// InvokeReminder implements actors.InternalActor
func (w *workflow) InvokeReminder(ctx context.Context, reminder *actorapi.Reminder) error {
	log.Debugf("Workflow actor '%s': invoking reminder '%s'", w.actorID, reminder.Name)

	completed, err := w.runWorkflow(ctx, reminder)

	if completed == runCompletedTrue {
		w.table.DeleteFromTableIn(w, time.Second*10)
	}

	// We delete the reminder on success and on non-recoverable errors.
	// Returning nil signals that we want the execution to be retried in the next period interval
	switch {
	case err == nil:
		if w.schedulerReminders {
			return nil
		}
		return actorerrors.ErrReminderCanceled
	case errors.Is(err, context.DeadlineExceeded):
		log.Warnf("Workflow actor '%s': execution timed-out and will be retried later: '%v'", w.actorID, err)
		return err
	case errors.Is(err, context.Canceled):
		log.Warnf("Workflow actor '%s': execution was canceled (process shutdown?) and will be retried later: '%v'", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return nil
	case wferrors.IsRecoverable(err):
		log.Warnf("Workflow actor '%s': execution failed with a recoverable error and will be retried later: '%v'", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return nil
	default: // Other error
		log.Errorf("Workflow actor '%s': execution failed with an error: %v", w.actorID, err)
		if w.schedulerReminders {
			return err
		}
		return actorerrors.ErrReminderCanceled
	}
}

// InvokeTimer implements actors.InternalActor
func (w *workflow) InvokeTimer(ctx context.Context, reminder *actorapi.Reminder) error {
	return errors.New("timers are not implemented")
}

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
	if _, err := w.createReliableReminder(ctx, "start", nil, 0); err != nil {
		return err
	}

	return nil
}

// This method cleans up a workflow associated with the given actorID
func (w *workflow) cleanupWorkflowStateInternal(ctx context.Context, state *wfenginestate.State, requiredAndNotCompleted bool) error {
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
	err = w.actorState.TransactionalStateOperation(ctx, true, req)
	if err != nil {
		return err
	}
	w.table.DeleteFromTable(w.actorType, w.actorID)
	w.cleanup()
	return nil
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

func (w *workflow) addWorkflowEvent(ctx context.Context, historyEventBytes []byte) error {
	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}

	var e backend.HistoryEvent
	err = proto.Unmarshal(historyEventBytes, &e)
	if e.GetTaskCompleted() != nil || e.GetTaskFailed() != nil {
		w.activityResultAwaited.CompareAndSwap(true, false)
	}
	if err != nil {
		return err
	}
	log.Debugf("Workflow actor '%s': adding event to the workflow inbox", w.actorID)
	state.AddToInbox(&e)

	if err := w.saveInternalState(ctx, state); err != nil {
		return err
	}

	if _, err := w.createReliableReminder(ctx, "new-event", nil, 0); err != nil {
		return err
	}

	return nil
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

func (w *workflow) runWorkflow(ctx context.Context, reminder *actorapi.Reminder) (runCompleted, error) {
	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return runCompletedTrue, fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		log.Warnf("No workflow state found for actor '%s', terminating execution", w.actorID)
		return runCompletedTrue, nil
	}

	if strings.HasPrefix(reminder.Name, "timer-") {
		var durableTimer backend.DurableTimer
		if err = reminder.Data.UnmarshalTo(&durableTimer); err != nil {
			// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
			return runCompletedTrue, err
		}

		if durableTimer.GetGeneration() < state.Generation {
			log.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", w.actorID, durableTimer.GetGeneration())
			return runCompletedFalse, nil
		}

		state.Inbox = append(state.Inbox, durableTimer.GetTimerEvent())
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		log.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", w.actorID, reminder.Name)
		return runCompletedFalse, nil
	}

	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	transactionalRequests := make(map[string][]actorapi.TransactionalOperation)
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
		op := actorapi.TransactionalOperation{
			Operation: actorapi.Delete,
			Request: actorapi.TransactionalDelete{
				Key: activityStateKey,
			},
		}
		activityActorID := getActivityActorID(w.actorID, taskID, state.Generation)
		if transactionalRequests[activityActorID] == nil {
			transactionalRequests[activityActorID] = []actorapi.TransactionalOperation{op}
		} else {
			transactionalRequests[activityActorID] = append(transactionalRequests[activityActorID], op)
		}
	}

	var lock sync.Mutex
	var wg sync.WaitGroup
	var errs []error

	wg.Add(len(transactionalRequests))
	for activityActorID, operations := range transactionalRequests {
		go func(activityActorID string, operations []actorapi.TransactionalOperation) {
			defer wg.Done()
			aerr := w.actorState.TransactionalStateOperation(ctx, true, &actorapi.TransactionalRequest{
				ActorType:  w.activityActorType,
				ActorID:    activityActorID,
				Operations: operations,
			})
			if aerr != nil {
				lock.Lock()
				errs = append(errs, fmt.Errorf("failed to delete activity state for activity actor '%s' with error: %w", activityActorID, aerr))
				lock.Unlock()
				return
			}
		}(activityActorID, operations)
	}
	wg.Wait()
	if len(errs) > 0 {
		return runCompletedFalse, errors.Join(errs...)
	}

	rs := w.rstate
	wi := &backend.OrchestrationWorkItem{
		InstanceID: api.InstanceID(rs.GetInstanceId()),
		NewEvents:  state.Inbox,
		RetryCount: -1, // TODO
		State:      rs,
		Properties: make(map[string]any, 1),
	}

	// Executing workflow code is a one-way operation. We must wait for the app code to report its completion, which
	// will trigger this callback channel.
	callback := make(chan bool, 1)
	wi.Properties[todo.CallbackChannelProperty] = callback
	// Setting executionStatus to failed by default to record metrics for non-recoverable errors.
	executionStatus := diag.StatusFailed
	if rs != nil && runtimestate.IsCompleted(rs) {
		// If workflow is already completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
	}
	workflowName := w.getExecutionStartedEvent(state).GetName()
	// Request to execute workflow
	log.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", w.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	// TODO: @joshvanl remove.
	err = w.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule a workflow execution: %w", err))
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
			return runCompletedFalse, wferrors.NewRecoverable(errExecutionAborted)
		}
	}
	log.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", w.actorID, runtimestate.RuntimeStatus(rs).String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if rs.GetContinuedAsNew() {
		log.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", w.actorID, wi.InstanceID)
		state.Generation += 1
	}

	if !runtimestate.IsCompleted(rs) {
		// Create reminders for the durable timers. We only do this if the orchestration is still running.
		for _, t := range rs.GetPendingTimers() {
			tf := t.GetTimerFired()
			if tf == nil {
				return runCompletedTrue, errors.New("invalid event in the PendingTimers list")
			}
			delay := time.Until(tf.GetFireAt().AsTime())
			if delay < 0 {
				delay = 0
			}
			reminderPrefix := "timer-" + strconv.Itoa(int(tf.GetTimerId()))
			data := &backend.DurableTimer{
				TimerEvent: t,
				Generation: state.Generation,
			}
			log.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer", w.actorID, reminderPrefix)
			if _, err = w.createReliableReminder(ctx, reminderPrefix, data, delay); err != nil {
				executionStatus = diag.StatusRecoverable
				return runCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("actor '%s' failed to create reminder for timer: %w", w.actorID, err))
			}
		}
	}

	// Process the outbound orchestrator events
	reqsByName := make(map[string][]*backend.OrchestrationRuntimeStateMessage, len(rs.GetPendingMessages()))
	for _, msg := range rs.GetPendingMessages() {
		if es := msg.GetHistoryEvent().GetExecutionStarted(); es != nil {
			reqsByName[todo.CreateWorkflowInstanceMethod] = append(reqsByName[todo.CreateWorkflowInstanceMethod], msg)
		} else if msg.GetHistoryEvent().GetSubOrchestrationInstanceCompleted() != nil || msg.GetHistoryEvent().GetSubOrchestrationInstanceFailed() != nil {
			reqsByName[todo.AddWorkflowEventMethod] = append(reqsByName[todo.AddWorkflowEventMethod], msg)
		} else {
			log.Warnf("Workflow actor '%s': don't know how to process outbound message '%v'", w.actorID, msg)
		}
	}

	var compl runCompleted

	// Schedule activities
	pendingTasks := rs.GetPendingTasks()
	wg.Add(len(pendingTasks))
	for _, e := range pendingTasks {
		go func(e *backend.HistoryEvent) {
			defer wg.Done()

			ts := e.GetTaskScheduled()
			if ts == nil {
				log.Warnf("Workflow actor '%s': unable to process task '%v'", w.actorID, e)
				return
			}

			var eventData []byte
			eventData, err = proto.Marshal(e)
			if err != nil {
				lock.Lock()
				compl = runCompletedTrue
				errs = append(errs, err)
				lock.Unlock()
				return
			}

			targetActorID := getActivityActorID(w.actorID, e.GetEventId(), state.Generation)

			w.activityResultAwaited.Store(true)

			log.Debugf("Workflow actor '%s': invoking execute method on activity actor '%s'", w.actorID, targetActorID)

			_, eerr := w.engine.Call(ctx, internalsv1pb.
				NewInternalInvokeRequest("Execute").
				WithActor(w.activityActorType, targetActorID).
				WithData(eventData).
				WithContentType(invokev1.ProtobufContentType),
			)

			if errors.Is(eerr, ErrDuplicateInvocation) {
				log.Warnf("Workflow actor '%s': activity invocation '%s::%d' was flagged as a duplicate and will be skipped", w.actorID, ts.GetName(), e.GetEventId())
				return
			} else if eerr != nil {
				lock.Lock()
				executionStatus = diag.StatusRecoverable
				errs = append(errs, fmt.Errorf("failed to invoke activity actor '%s' to execute '%s': %w", targetActorID, ts.GetName(), eerr))
				lock.Unlock()
				return
			}
		}(e)
	}

	wg.Wait()
	if len(errs) > 0 {
		return compl, errors.Join(errs...)
	}

	for method, msgList := range reqsByName {
		wg.Add(len(msgList))
		for _, msg := range msgList {
			go func(method string, msg *backend.OrchestrationRuntimeStateMessage) {
				defer wg.Done()

				var requestBytes []byte
				var perr error
				if method == todo.CreateWorkflowInstanceMethod {
					requestBytes, perr = proto.Marshal(&backend.CreateWorkflowInstanceRequest{
						StartEvent: msg.GetHistoryEvent(),
					})
					if perr != nil {
						lock.Lock()
						errs = append(errs, fmt.Errorf("failed to marshal createWorkflowInstanceRequest: %w", perr))
						compl = runCompletedTrue
						lock.Unlock()
						return
					}
				} else {
					requestBytes, perr = proto.Marshal(msg.GetHistoryEvent())
					if perr != nil {
						lock.Lock()
						errs = append(errs, perr)
						compl = runCompletedTrue
						lock.Unlock()
						return
					}
				}

				log.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", w.actorID, method, msg.GetTargetInstanceID())

				_, eerr := w.engine.Call(ctx, internalsv1pb.
					NewInternalInvokeRequest(method).
					WithActor(w.actorType, msg.GetTargetInstanceID()).
					WithData(requestBytes).
					WithContentType(invokev1.ProtobufContentType),
				)
				if eerr != nil {
					lock.Lock()
					executionStatus = diag.StatusRecoverable
					errs = append(errs, fmt.Errorf("failed to invoke method '%s' on actor '%s': %w", method, msg.GetTargetInstanceID(), eerr))
					lock.Unlock()
					return
				}
			}(method, msg)
		}
	}

	wg.Wait()
	if len(errs) > 0 {
		return compl, errors.Join(errs...)
	}

	state.ApplyRuntimeStateChanges(rs)
	state.ClearInbox()

	err = w.saveInternalState(ctx, state)
	if err != nil {
		return runCompletedTrue, err
	}
	if executionStatus != "" {
		// If workflow is not completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
		if runtimestate.IsCompleted(rs) {
			if runtimestate.RuntimeStatus(rs) == api.RUNTIME_STATUS_COMPLETED {
				executionStatus = diag.StatusSuccess
			} else {
				// Setting executionStatus to failed if workflow has failed/terminated/cancelled
				executionStatus = diag.StatusFailed
			}
			wfExecutionElapsedTime = w.calculateWorkflowExecutionLatency(state)
		}
	}
	if runtimestate.IsCompleted(rs) {
		log.Infof("Workflow Actor '%s': workflow completed with status '%s' workflowName '%s'", w.actorID, runtimestate.RuntimeStatus(rs).String(), workflowName)
		return runCompletedTrue, nil
	}
	return runCompletedFalse, nil
}

func (*workflow) calculateWorkflowExecutionLatency(state *wfenginestate.State) (wExecutionElapsedTime float64) {
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

	if err = w.actorState.TransactionalStateOperation(ctx, true, req); err != nil {
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

func (w *workflow) createReliableReminder(ctx context.Context, namePrefix string, data proto.Message, delay time.Duration) (string, error) {
	b := make([]byte, 6)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return "", fmt.Errorf("failed to generate reminder ID: %w", err)
	}

	reminderName := namePrefix + "-" + base64.RawURLEncoding.EncodeToString(b)
	log.Debugf("Workflow actor '%s||%s': creating '%s' reminder with DueTime = '%s'", w.activityActorType, w.actorID, reminderName, delay)

	var period string
	var oneshot bool
	if w.schedulerReminders {
		oneshot = true
	} else {
		period = w.reminderInterval.String()
	}

	var adata *anypb.Any
	if data != nil {
		adata, err = anypb.New(data)
		if err != nil {
			return "", err
		}
	}

	return reminderName, w.reminders.Create(ctx, &actorapi.CreateReminderRequest{
		ActorType: w.actorType,
		ActorID:   w.actorID,
		Data:      adata,
		DueTime:   delay.String(),
		Name:      reminderName,
		Period:    period,
		IsOneShot: oneshot,
	})
}

func getActivityActorID(workflowID string, taskID int32, generation uint64) string {
	// An activity can be identified by its name followed by its task ID and generation. Example: SayHello::0::1, SayHello::1::1, etc.
	return workflowID + "::" + strconv.Itoa(int(taskID)) + "::" + strconv.FormatUint(generation, 10)
}

func (w *workflow) removeCompletedStateData(ctx context.Context, state *wfenginestate.State) error {
	var lock sync.Mutex
	var wg sync.WaitGroup
	var errs []error

	// The logic/for loop below purges/removes any leftover state from a completed or failed activity
	wg.Add(len(state.Inbox))
	for _, e := range state.Inbox {
		go func(e *backend.HistoryEvent) {
			defer wg.Done()

			var taskID int32
			if ts := e.GetTaskCompleted(); ts != nil {
				taskID = ts.GetTaskScheduledId()
			} else if tf := e.GetTaskFailed(); tf != nil {
				taskID = tf.GetTaskScheduledId()
			} else {
				return
			}

			req := actorapi.TransactionalRequest{
				ActorType: w.activityActorType,
				ActorID:   getActivityActorID(w.actorID, taskID, state.Generation),
				Operations: []actorapi.TransactionalOperation{{
					Operation: actorapi.Delete,
					Request: actorapi.TransactionalDelete{
						Key: activityStateKey,
					},
				}},
			}
			if terr := w.actorState.TransactionalStateOperation(ctx, true, &req); terr != nil {
				lock.Lock()
				errs = append(errs, fmt.Errorf("failed to delete activity state with error: %w", terr))
				lock.Unlock()
				return
			}
		}(e)
	}

	wg.Wait()

	return errors.Join(errs...)
}

// DeactivateActor implements actors.InternalActor
func (w *workflow) Deactivate() error {
	w.cleanup()
	log.Debugf("Workflow actor '%s': deactivated", w.actorID)
	return nil
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

func (w *workflow) InvokeStream(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	if err := w.handleStreamInitial(ctx, req, stream); err != nil {
		return err
	}

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan *backend.OrchestrationMetadata)
	w.ometaBroadcaster.Subscribe(subCtx, ch)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.closeCh:
			return nil
		case val, ok := <-ch:
			if !ok {
				return nil
			}
			d, err := anypb.New(val)
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-w.closeCh:
				return nil
			case stream <- &internalsv1pb.InternalInvokeResponse{
				Status:  &internalsv1pb.Status{Code: http.StatusOK},
				Message: &commonv1pb.InvokeResponse{Data: d},
			}:
			}
		}
	}
}

func (w *workflow) handleStreamInitial(ctx context.Context, req *internalsv1pb.InternalInvokeRequest, stream chan<- *internalsv1pb.InternalInvokeResponse) error {
	if m := req.GetMessage().GetMethod(); m != todo.WaitForRuntimeStatus {
		return fmt.Errorf("unsupported stream method: %s", m)
	}

	_, ometa, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if ometa != nil {
		arstate, err := anypb.New(ometa)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
		case stream <- &internalsv1pb.InternalInvokeResponse{
			Status:  &internalsv1pb.Status{Code: http.StatusOK},
			Message: &commonv1pb.InvokeResponse{Data: arstate},
		}:
		}

		if api.OrchestrationMetadataIsComplete(ometa) {
			w.table.DeleteFromTableIn(w, time.Second*10)
		}
	}

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

// Key returns the key for this unique actor.
func (w *workflow) Key() string {
	return w.actorType + actorapi.DaprSeparator + w.actorID
}

// Type returns the type for this unique actor.
func (w *workflow) Type() string {
	return w.actorType
}

// ID returns the ID for this unique actor.
func (w *workflow) ID() string {
	return w.actorID
}
