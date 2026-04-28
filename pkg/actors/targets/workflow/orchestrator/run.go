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
	"maps"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/errors"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (o *orchestrator) runWorkflow(ctx context.Context, reminder *actorapi.Reminder) (todo.RunCompleted, error) {
	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return todo.RunCompletedTrue, fmt.Errorf("error loading internal state: %w", err)
	}
	if state == nil {
		// The assumption is that someone manually deleted the workflow state. This is non-recoverable.
		log.Warnf("No workflow state found for actor '%s', terminating execution", o.actorID)
		return todo.RunCompletedTrue, nil
	}

	if strings.HasPrefix(reminder.Name, "timer-") && !runtimestate.IsCompleted(o.rstate) {
		var durableTimer backend.DurableTimer
		if err = reminder.Data.UnmarshalTo(&durableTimer); err != nil {
			// Likely the result of an incompatible durable task timer format change.
			// This is non-recoverable.
			return todo.RunCompletedTrue, err
		}

		if durableTimer.GetGeneration() < state.Generation {
			log.Infof("Workflow actor '%s': ignoring durable timer from previous generation '%v'", o.actorID, durableTimer.GetGeneration())
			return todo.RunCompletedFalse, nil
		}

		timerEvent := durableTimer.GetTimerEvent()
		// Validate the timer event is actually a TimerFired event. A crafted
		// reminder could contain arbitrary event types to inject into the inbox.
		if timerEvent.GetTimerFired() == nil {
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': timer reminder contains non-TimerFired event type %T", o.actorID, timerEvent.GetEventType())
		}
		// timer fired event is precreated at the moment of creating the timer
		// set the timestamp to now so it is accurately recorded in the history
		timerEvent.Timestamp = timestamppb.Now()
		state.Inbox = append(state.Inbox, timerEvent)
	}

	if len(state.Inbox) == 0 {
		// This can happen after multiple events are processed in batches; there may still be reminders around
		// for some of those already processed events.
		log.Debugf("Workflow actor '%s': ignoring run request for reminder '%s' because the workflow inbox is empty", o.actorID, reminder.Name)
		return todo.RunCompletedTrue, nil
	}

	var esHistoryEvent *backend.HistoryEvent
	for _, e := range state.Inbox {
		if es := e.GetExecutionStarted(); es != nil {
			esHistoryEvent = e
			if esHistoryEvent.Router == nil {
				// Set the source app ID for cross-app routing in durabletask-go
				esHistoryEvent.Router = &protos.TaskRouter{
					SourceAppID: o.appID,
				}
			}
			break
		}
	}

	rs := o.rstate
	wi := &backend.WorkflowWorkItem{
		InstanceID: api.InstanceID(rs.GetInstanceId()),
		NewEvents:  state.Inbox,
		RetryCount: -1, // TODO
		State:      rs,
		Properties: make(map[string]any, 2),
	}

	wi.IncomingHistory = state.IncomingHistory
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
	workflowName := o.getExecutionStartedEvent(state).GetName()
	// Request to execute workflow
	log.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", o.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	// Snapshot o.rstate before the engine runs. The engine shares wi.State
	// with o.rstate (same pointer) and may overwrite it during ContinueAsNew
	// (*s = *newState in the applier). If the engine fails, we restore the
	// snapshot so the cached state remains consistent with the store.
	var rstateSnapshot *backend.WorkflowRuntimeState
	if o.rstate != nil {
		rstateSnapshot = proto.Clone(o.rstate).(*backend.WorkflowRuntimeState)
	}

	// TODO: @joshvanl remove.
	err = o.scheduler(ctx, wi)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("timed-out trying to schedule a workflow execution - this can happen if there are too many in-flight workflows or if the workflow engine isn't running: %w", err))
		}
		return todo.RunCompletedFalse, wferrors.NewRecoverable(fmt.Errorf("failed to schedule a workflow execution: %w", err))
	}

	o.recordWorkflowSchedulingLatency(ctx, esHistoryEvent, workflowName)
	wfExecutionElapsedTime := float64(0)

	defer func() {
		if executionStatus != "" {
			diag.DefaultWorkflowMonitoring.WorkflowExecutionEvent(ctx, workflowName, executionStatus)
			diag.DefaultWorkflowMonitoring.WorkflowExecutionLatency(ctx, workflowName, executionStatus, wfExecutionElapsedTime)
		}
	}()

	select {
	case <-ctx.Done(): // caller is responsible for timeout management
		// The engine may have partially mutated o.rstate via the shared
		// wi.State pointer before the context was cancelled. Restore the
		// snapshot so the cached state stays consistent with the store.
		o.rstate = rstateSnapshot
		executionStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, ctx.Err()
	case completed := <-callback:
		if !completed {
			// The engine abandoned this work item (e.g. MaxContinueAsNewCount
			// exceeded). The engine's ContinueAsNew tight-loop may have
			// overwritten o.rstate via the shared wi.State pointer
			// (*s = *newState in the applier). If CAN progress was made,
			// persist it to the state store so it survives actor
			// deactivation. Carryover events (unprocessed EventRaised
			// events from the CAN state) are moved to the Inbox so they
			// become NewEvents on retry. The stale inbox (which contained
			// ALL original events including those already consumed) is
			// replaced to prevent duplicate event delivery.
			// If no CAN progress was made (non-CAN failure), restore the
			// pre-engine snapshot so the cached state stays consistent.
			if wi.State.GetContinuedAsNew() {
				// Separate carryover EventRaised events from the CAN
				// execution events (WorkflowStarted, ExecutionStarted).
				// Carryover events must go into the Inbox so they become
				// NewEvents on retry. If they stay in History (as
				// OldEvents) alongside the original Inbox events
				// (NewEvents), the engine would buffer both sets and the
				// workflow would process duplicate events.
				canNewEvents := wi.State.GetNewEvents()
				filtered := make([]*backend.HistoryEvent, 0, len(canNewEvents))
				var carryover []*backend.HistoryEvent
				for _, e := range canNewEvents {
					if e.GetEventRaised() != nil {
						carryover = append(carryover, e)
					} else {
						filtered = append(filtered, e)
					}
				}

				// Temporarily swap NewEvents so ApplyRuntimeStateChanges
				// only writes the CAN execution events to History.
				if len(carryover) > 0 {
					wi.State.NewEvents = filtered
					state.ApplyRuntimeStateChanges(wi.State)
					wi.State.NewEvents = canNewEvents

					state.ClearInbox()
					for _, e := range carryover {
						state.AddToInbox(e)
					}
				} else {
					state.ApplyRuntimeStateChanges(wi.State)
				}

				state.Generation++

				// The engine carries the propagation chain across CAN by
				// updating wi.IncomingHistory. Persist any change so the new
				// generation observes the chain on its next run.
				if wi.IncomingHistory != state.IncomingHistory {
					state.SetIncomingHistory(wi.IncomingHistory)
				}

				if err = o.signAndSaveState(ctx, state); err != nil {
					o.rstate = rstateSnapshot
					return todo.RunCompletedFalse, err
				}

				if len(carryover) > 0 {
					if _, err = o.createWorkflowReminder(ctx, reminderPrefixNewEvent, nil, time.Now(), o.appID, &workflowName); err != nil {
						return todo.RunCompletedFalse, err
					}
				}
			} else {
				o.rstate = rstateSnapshot
			}
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(todo.ErrExecutionAborted)
		}
	}
	rs = wi.State

	if err = o.handleStalled(ctx, state, rs); err != nil {
		return todo.RunCompletedFalse, err
	}
	compactPatches(rs)

	runtimeStatus := runtimestate.RuntimeStatus(rs)
	log.Debugf("Workflow actor '%s': workflow execution returned with status '%s' instanceId '%s'", o.actorID, runtimeStatus.String(), wi.InstanceID)

	// Increment the generation counter if the workflow used continue-as-new. Subsequent actions below
	// will use this updated generation value for their duplication execution handling.
	if rs.GetContinuedAsNew() {
		log.Debugf("Workflow actor '%s': workflow with instanceId '%s' continued as new", o.actorID, wi.InstanceID)
		state.Generation += 1
		// The engine carries the propagation chain across CAN by updating
		// wi.IncomingHistory. Persist any change so the new generation sees
		// the chain on its next run.
		if wi.IncomingHistory != state.IncomingHistory {
			state.SetIncomingHistory(wi.IncomingHistory)
		}
	}

	if !runtimestate.IsCompleted(rs) {
		// Delete timer reminders for WaitForSingleEvent timers where the event has
		// been received before the timer fired.
		if err = o.deleteCancelledEventTimers(ctx, rs); err != nil {
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
		}

		if err = o.createTimers(ctx, rs.GetPendingTimers(), state.Generation); err != nil {
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(err)
		}
	}

	pendingTasks := rs.GetPendingTasks()

	// Process the outbound orchestrator events.
	var addWorkflows []*backend.WorkflowRuntimeStateMessage
	var createWorkflows []*backend.WorkflowRuntimeStateMessage
	for _, msg := range rs.GetPendingMessages() {
		switch {
		case msg.GetHistoryEvent().GetExecutionStarted() != nil:
			createWorkflows = append(createWorkflows, msg)

		case msg.GetHistoryEvent().GetChildWorkflowInstanceCompleted() != nil, msg.GetHistoryEvent().GetChildWorkflowInstanceFailed() != nil:
			addWorkflows = append(addWorkflows, msg)

		default:
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': don't know how to process outbound message '%v'", o.actorID, msg)
		}
	}

	// Dispatch activities and messages, collecting failures.
	activityResult := o.callActivities(ctx, pendingTasks, state, wi.OutgoingHistory)
	addResult := o.callAddEventStateMessage(ctx, addWorkflows)
	createResult := o.callCreateWorkflowStateMessage(ctx, createWorkflows)

	dispatchErr := errors.Join(activityResult.err, addResult.err, createResult.err)
	if dispatchErr != nil {
		if len(state.History) == 0 && (hasRemoteTasks(pendingTasks) || hasRemoteMessages(createWorkflows)) {
			// Save state without the events that failed to dispatch so the
			// workflow transitions to RUNNING. Successfully dispatched items
			// keep their events in history so they are not re-dispatched on
			// retry. The inbox is preserved so the existing reminder retries
			// the full execution.
			allFailed := make(map[int32]struct{}, len(activityResult.failedEventIDs)+len(createResult.failedEventIDs)+len(addResult.failedEventIDs))
			maps.Copy(allFailed, activityResult.failedEventIDs)
			maps.Copy(allFailed, createResult.failedEventIDs)
			maps.Copy(allFailed, addResult.failedEventIDs)

			// Temporarily replace rs.NewEvents with a filtered copy that excludes
			// failed dispatch events, then restore the original after
			// ApplyRuntimeStateChanges. This works because ApplyRuntimeStateChanges
			// reads rs.NewEvents by reference (via GetNewEvents()) and appends
			// directly to state.History. It does not copy or retain the slice.
			origNewEvents := rs.NewEvents
			filtered := origNewEvents[:0:0]
			for _, e := range origNewEvents {
				if isDispatchableEvent(e) {
					if _, failed := allFailed[e.GetEventId()]; failed {
						continue
					}
				}
				filtered = append(filtered, e)
			}
			rs.NewEvents = filtered
			state.ApplyRuntimeStateChanges(rs)
			rs.NewEvents = origNewEvents
			if saveErr := o.signAndSaveState(ctx, state); saveErr != nil {
				return todo.RunCompletedFalse, saveErr
			}
			executionStatus = diag.StatusRecoverable
			return todo.RunCompletedFalse, wferrors.NewRecoverable(dispatchErr)
		}

		executionStatus = diag.StatusRecoverable
		return todo.RunCompletedFalse, wferrors.NewRecoverable(dispatchErr)
	}

	state.ApplyRuntimeStateChanges(rs)
	state.ClearInbox()

	err = o.signAndSaveState(ctx, state)
	if err != nil {
		return todo.RunCompletedFalse, err
	}

	rstatus := runtimestate.RuntimeStatus(rs)
	if executionStatus != "" {
		// If workflow is not completed, set executionStatus to empty string
		// which will skip recording metrics for this execution.
		executionStatus = ""
		if runtimestate.IsCompleted(rs) {
			if rstatus == api.RUNTIME_STATUS_COMPLETED {
				executionStatus = diag.StatusSuccess
			} else {
				// Setting executionStatus to failed if workflow has failed/terminated/cancelled
				executionStatus = diag.StatusFailed
			}
			wfExecutionElapsedTime = o.calculateWorkflowExecutionLatency(state)
		}
	}

	if runtimestate.IsCompleted(rs) {
		log.Infof("Workflow Actor '%s': workflow completed with status '%s' workflowName '%s'", o.actorID, rstatus, workflowName)
		if hasUnfiredTimers(rs) {
			if err = o.deleteAllReminders(ctx); err != nil {
				return todo.RunCompletedFalse, err
			}
		}
		if err = o.handleRetention(ctx, rstatus); err != nil {
			return todo.RunCompletedFalse, err
		}
		return todo.RunCompletedTrue, nil
	}

	return todo.RunCompletedFalse, nil
}

func (*orchestrator) calculateWorkflowExecutionLatency(state *wfenginestate.State) (wExecutionElapsedTime float64) {
	for _, e := range state.History {
		if os := e.GetWorkflowStarted(); os != nil {
			return diag.ElapsedSince(e.GetTimestamp().AsTime())
		}
	}
	return 0
}

func (*orchestrator) recordWorkflowSchedulingLatency(ctx context.Context, esHistoryEvent *backend.HistoryEvent, workflowName string) {
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

func (o *orchestrator) handleRetention(ctx context.Context, status protos.OrchestrationStatus) error {
	if o.retentionPolicy == nil {
		return nil
	}

	var dueTime *time.Duration
	var name string
	switch {
	case o.retentionPolicy.Completed != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED:
		dueTime = o.retentionPolicy.Completed
		name = "completed"
	case o.retentionPolicy.Terminated != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED:
		dueTime = o.retentionPolicy.Terminated
		name = "terminated"
	case o.retentionPolicy.Failed != nil &&
		status == protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED:
		dueTime = o.retentionPolicy.Failed
		name = "failed"
	case o.retentionPolicy.AnyTerminal != nil:
		dueTime = o.retentionPolicy.AnyTerminal
		name = "anyterminal"
	}

	if dueTime != nil {
		log.Debugf("Workflow actor '%s': setting retention reminder for status '%s' with due time '%v'", o.actorID, status.String(), dueTime)
		_, err := o.createRetentionReminder(ctx, name, time.Now().Add(*dueTime))
		return err
	}

	return nil
}

// filterValidInboxEvents returns inbox events that pass validation. Result
// events (TaskCompleted/TaskFailed, ChildWorkflowInstanceCompleted/Failed)
// must match operations that were scheduled in signed history. Invalid events
// are dropped and logged.
func filterValidInboxEvents(state *wfenginestate.State) []*backend.HistoryEvent {
	// Build sets of scheduled operation event IDs from history.
	scheduledTaskIDs := make(map[int32]struct{})
	createdChildIDs := make(map[int32]struct{})
	for _, e := range state.History {
		switch {
		case e.GetTaskScheduled() != nil:
			scheduledTaskIDs[e.GetEventId()] = struct{}{}
		case e.GetChildWorkflowInstanceCreated() != nil:
			createdChildIDs[e.GetEventId()] = struct{}{}
		}
	}

	valid := make([]*backend.HistoryEvent, 0, len(state.Inbox))
	for _, e := range state.Inbox {
		// exhaustive linter will error here on missing types not implemented on
		// the switch.
		switch et := e.GetEventType().(type) {
		case *protos.HistoryEvent_TaskCompleted:
			taskID := et.TaskCompleted.GetTaskScheduledId()
			if _, ok := scheduledTaskIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: task result for task %d not scheduled in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_TaskFailed:
			taskID := et.TaskFailed.GetTaskScheduledId()
			if _, ok := scheduledTaskIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: task failure for task %d not scheduled in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_ChildWorkflowInstanceCompleted:
			taskID := et.ChildWorkflowInstanceCompleted.GetTaskScheduledId()
			if _, ok := createdChildIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: child workflow result for task %d not created in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_ChildWorkflowInstanceFailed:
			taskID := et.ChildWorkflowInstanceFailed.GetTaskScheduledId()
			if _, ok := createdChildIDs[taskID]; !ok {
				log.Warnf("Dropping injected inbox event: child workflow failure for task %d not created in signed history", taskID)
				continue
			}
		case *protos.HistoryEvent_EventRaised,
			*protos.HistoryEvent_TimerFired,
			*protos.HistoryEvent_ExecutionStarted,
			*protos.HistoryEvent_ExecutionTerminated,
			*protos.HistoryEvent_ExecutionResumed,
			*protos.HistoryEvent_ExecutionSuspended:
			// Legitimate inbox event types that do not correspond to a previously
			// scheduled operation.
		default:
			log.Warnf("Dropping injected inbox event: unknown event type %T", et)
			continue
		}
		valid = append(valid, e)
	}

	return valid
}
