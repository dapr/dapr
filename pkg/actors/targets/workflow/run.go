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
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (w *workflow) runWorkflow(ctx context.Context, reminder *actorapi.Reminder) (todo.RunCompleted, error) {
	state, _, err := w.loadInternalState(ctx)
	if err != nil {
		return todo.RunCompletedTrue, fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		log.Warnf("No workflow state found for actor '%s', terminating execution", w.actorID)
		return todo.RunCompletedTrue, nil
	}

	if strings.HasPrefix(reminder.Name, "timer-") {
		var durableTimer backend.DurableTimer
		if err = reminder.Data.UnmarshalTo(&durableTimer); err != nil {
			// Likely the result of an incompatible durable task timer format change. This is non-recoverable.
			return todo.RunCompletedTrue, err
		}

		if durableTimer.GetGeneration() < state.Generation {
			log.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", w.actorID, durableTimer.GetGeneration())
			return todo.RunCompletedFalse, nil
		}

		state.Inbox = append(state.Inbox, durableTimer.GetTimerEvent())
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		log.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", w.actorID, reminder.Name)
		return todo.RunCompletedFalse, nil
	}

	var esHistoryEvent *backend.HistoryEvent

	for _, e := range state.Inbox {
		if es := e.GetExecutionStarted(); es != nil {
			esHistoryEvent = e
			break
		}
	}
	// likely need to set task router here
	rs := w.rstate
	wi := &backend.OrchestrationWorkItem{
		InstanceID: api.InstanceID(rs.GetInstanceId()),
		NewEvents:  state.Inbox,
		RetryCount: -1, // TODO
		State:      rs,
		Properties: make(map[string]any, 1),
	}

	// Set the source app ID for cross-app routing in durabletask-go
	if esHistoryEvent != nil && esHistoryEvent.Router != nil {
		esHistoryEvent.Router.Source = w.appID
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
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule a workflow execution: %w", err))
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
		return todo.RunCompletedFalse, ctx.Err()
	case completed := <-callback:
		if !completed {
			// Workflow execution failed with recoverable error
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(todo.ErrExecutionAborted)
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
				return todo.RunCompletedTrue, errors.New("invalid event in the PendingTimers list")
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

			// TODO: confirm if i need to check router here
			//var targetAppID string
			//if router := t.GetRouter(); router != nil {
			//	targetAppID = router.GetTarget()
			//}

			log.Debugf("Workflow actor '%s': creating reminder '%s' for the durable timer", w.actorID, reminderPrefix)
			//if _, err = w.createReminder(ctx, reminderPrefix, data, delay, targetAppID); err != nil {
			if _, err = w.createReminder(ctx, reminderPrefix, data, delay, w.appID); err != nil {
				executionStatus = diag.StatusRecoverable
				return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("actor '%s' failed to create reminder for timer: %w", w.actorID, err))
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

	err = w.callActivities(ctx, rs.GetPendingTasks(), state.Generation)
	if err != nil {
		executionStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, err
	}

	var (
		compl todo.RunCompleted
		wg    sync.WaitGroup
		lock  sync.Mutex
		errs  []error
	)

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
						compl = todo.RunCompletedTrue
						lock.Unlock()
						return
					}
				} else {
					requestBytes, perr = proto.Marshal(msg.GetHistoryEvent())
					if perr != nil {
						lock.Lock()
						errs = append(errs, perr)
						compl = todo.RunCompletedTrue
						lock.Unlock()
						return
					}
				}

				log.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s'", w.actorID, method, msg.GetTargetInstanceID())

				_, eerr := w.router.Call(ctx, internalsv1pb.
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
		return todo.RunCompletedTrue, err
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
		return todo.RunCompletedTrue, nil
	}
	return todo.RunCompletedFalse, nil
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
