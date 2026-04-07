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
	workflowName := o.getExecutionStartedEvent(state).GetName()
	// Request to execute workflow
	log.Debugf("Workflow actor '%s': scheduling workflow execution with instanceId '%s'", o.actorID, wi.InstanceID)
	// Schedule the workflow execution by signaling the backend
	// Snapshot o.rstate before the engine runs. The engine shares wi.State
	// with o.rstate (same pointer) and may overwrite it during ContinueAsNew
	// (*s = *newState in the applier). If the engine fails, we restore the
	// snapshot so the cached state remains consistent with the store.
	var rstateSnapshot *backend.OrchestrationRuntimeState
	if o.rstate != nil {
		rstateSnapshot = proto.Clone(o.rstate).(*backend.OrchestrationRuntimeState)
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
			// deactivation. The inbox is NOT cleared — it still contains
			// the original events. On retry, the engine replays from the
			// saved CAN progress and re-processes the inbox, skipping
			// already-consumed events via deduplication.
			// If no CAN progress was made (non-CAN failure), restore the
			// pre-engine snapshot so the cached state stays consistent.
			if wi.State.GetContinuedAsNew() {
				state.ApplyRuntimeStateChanges(wi.State)
				state.Generation++
				if err = o.saveInternalState(ctx, state); err != nil {
					return todo.RunCompletedFalse, err
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
	var addWorkflows []*backend.OrchestrationRuntimeStateMessage
	var createWorkflows []*backend.OrchestrationRuntimeStateMessage
	for _, msg := range rs.GetPendingMessages() {
		switch {
		case msg.GetHistoryEvent().GetExecutionStarted() != nil:
			createWorkflows = append(createWorkflows, msg)

		case msg.GetHistoryEvent().GetSubOrchestrationInstanceCompleted() != nil, msg.GetHistoryEvent().GetSubOrchestrationInstanceFailed() != nil:
			addWorkflows = append(addWorkflows, msg)

		default:
			return todo.RunCompletedTrue, fmt.Errorf("workflow actor '%s': don't know how to process outbound message '%v'", o.actorID, msg)
		}
	}

	// Dispatch activities and messages, collecting failures.
	activityResult := o.callActivities(ctx, pendingTasks, state)
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
			if saveErr := o.saveInternalState(ctx, state); saveErr != nil {
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

	err = o.saveInternalState(ctx, state)
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
		if os := e.GetOrchestratorStarted(); os != nil {
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
