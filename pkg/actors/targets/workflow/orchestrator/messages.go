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

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

// detachedSpawnDeniedError signals that a detached workflow spawn was
// rejected by the target app's WorkflowAccessPolicy. The dispatch path
// returns this so run.go can fail the parent terminally instead of
// silently dropping the action — the orchestrator function has already
// returned synchronously from ScheduleNewWorkflow with a non-empty
// instance ID, so the operator's only signal that something went wrong
// is the parent's terminal status.
type detachedSpawnDeniedError struct {
	instanceID  string
	targetAppID string
	cause       error
}

func (e *detachedSpawnDeniedError) Error() string {
	return fmt.Sprintf("detached workflow spawn '%s' denied by access policy on app '%s': %v", e.instanceID, e.targetAppID, e.cause)
}

func (e *detachedSpawnDeniedError) Unwrap() error { return e.cause }

// failParentDetachedDenied marks the parent orchestration as terminally
// FAILED in response to a detached spawn that was rejected by the target
// app's WorkflowAccessPolicy. The orchestrator function may have already
// staged an ExecutionCompletedEvent (success) for this run; we override
// it with a FAILED variant so the persisted terminal state reflects the
// fact that the orchestration could not fulfill its scheduling intent.
// Returns nil so the caller can mark the reminder as completed and stop
// retrying — the denial is permanent.
func (o *orchestrator) failParentDetachedDenied(ctx context.Context, state *wfenginestate.State, rs *backend.WorkflowRuntimeState, denyErr *detachedSpawnDeniedError) error {
	failureDetails := &protos.TaskFailureDetails{
		ErrorType:    "WorkflowAccessPolicyDenied",
		ErrorMessage: denyErr.Error(),
	}
	completed := &protos.ExecutionCompletedEvent{
		WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
		FailureDetails: failureDetails,
	}
	rs.CompletedEvent = completed
	rs.CompletedTime = timestamppb.Now()

	// Replace the orchestrator's pending ExecutionCompletedEvent (if any)
	// with the FAILED variant so caller history records the same terminal
	// status that rs.CompletedEvent points to. If the orchestrator did not
	// emit a terminal event this run (rare — only with mid-flight dispatch
	// of a fresh-state remote action), append one so the workflow lands in
	// a terminal state instead of stalling.
	var replaced bool
	for i := range rs.NewEvents {
		if rs.NewEvents[i].GetExecutionCompleted() != nil {
			rs.NewEvents[i] = &protos.HistoryEvent{
				EventId:   rs.NewEvents[i].GetEventId(),
				Timestamp: rs.NewEvents[i].GetTimestamp(),
				EventType: &protos.HistoryEvent_ExecutionCompleted{
					ExecutionCompleted: completed,
				},
			}
			replaced = true
			break
		}
	}
	if !replaced {
		rs.NewEvents = append(rs.NewEvents, &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionCompleted{
				ExecutionCompleted: completed,
			},
		})
	}

	state.ApplyRuntimeStateChanges(rs)
	if err := o.signAndSaveState(ctx, state); err != nil {
		return fmt.Errorf("failed to persist terminal failure for denied detached spawn: %w", err)
	}

	log.Warnf("Workflow actor '%s': failing parent terminally because detached workflow spawn was denied by access policy: %v", o.actorID, denyErr)
	return nil
}

func (o *orchestrator) callCreateWorkflowStateMessage(ctx context.Context, events []*backend.WorkflowRuntimeStateMessage, newEvents []*backend.HistoryEvent) dispatchResult {
	msgs := make([]proto.Message, len(events))
	historyEvents := make([]*backend.HistoryEvent, len(events))
	targets := make([]string, len(events))
	actionIDs := make([]int32, len(events))

	// Detached spawns dispatch a fresh ExecutionStartedEvent (EventId=-1, no
	// ParentInstance) but the caller-side action is recorded as a
	// DetachedWorkflowInstanceCreatedEvent whose EventId is the originating
	// action.Id and whose payload InstanceId equals the spawn target. The
	// applier emits the pair atomically inside the same NewEvents batch, so
	// we can recover the action.Id by indexing the batch by InstanceId. This
	// is what failed-dispatch recovery in run.go uses to drop just-failed
	// events from history before the partial save.
	var detachedActionByInstance map[string]int32
	for _, e := range newEvents {
		if dw := e.GetDetachedWorkflowInstanceCreated(); dw != nil {
			if detachedActionByInstance == nil {
				detachedActionByInstance = make(map[string]int32, 1)
			}
			detachedActionByInstance[dw.GetInstanceId()] = e.GetEventId()
		}
	}

	for i, msg := range events {
		req := &backend.CreateWorkflowInstanceRequest{StartEvent: msg.GetHistoryEvent()}
		if ph := msg.GetPropagatedHistory(); ph != nil {
			if o.signer == nil {
				log.Warnf("Workflow actor '%s': propagating unsigned workflow history to child workflow '%s' (signing is not configured; chunks cannot be cryptographically verified by the receiver)", o.actorID, msg.GetTargetInstanceId())
			}
			req.PropagatedHistory = ph
		}
		msgs[i] = req
		historyEvents[i] = msg.GetHistoryEvent()
		targets[i] = msg.GetTargetInstanceId()
		switch {
		case msg.GetHistoryEvent().GetExecutionStarted().GetParentInstance() != nil:
			actionIDs[i] = msg.GetHistoryEvent().GetExecutionStarted().GetParentInstance().GetTaskScheduledId()
		case detachedActionByInstance != nil:
			if id, ok := detachedActionByInstance[msg.GetTargetInstanceId()]; ok {
				actionIDs[i] = id
			} else {
				actionIDs[i] = msg.GetHistoryEvent().GetEventId()
			}
		default:
			actionIDs[i] = msg.GetHistoryEvent().GetEventId()
		}
	}

	return o.callStateMessages(ctx, msgs, historyEvents, targets, actionIDs, todo.CreateWorkflowInstanceMethod)
}

func (o *orchestrator) callAddEventStateMessage(ctx context.Context, events []*backend.WorkflowRuntimeStateMessage) dispatchResult {
	msgs := make([]proto.Message, len(events))
	historyEvents := make([]*backend.HistoryEvent, len(events))
	targets := make([]string, len(events))

	for i, msg := range events {
		msgs[i] = msg.GetHistoryEvent()
		historyEvents[i] = msg.GetHistoryEvent()
		targets[i] = msg.GetTargetInstanceId()
	}

	return o.callStateMessages(ctx, msgs, historyEvents, targets, nil, todo.AddWorkflowEventMethod)
}

func (o *orchestrator) callStateMessages(ctx context.Context, msgs []proto.Message, historyEvents []*backend.HistoryEvent, targets []string, actionIDs []int32, method string) dispatchResult {
	var result dispatchResult
	for i, msg := range msgs {
		if err := o.callStateMessage(ctx, msg, historyEvents[i], targets[i], method); err != nil {
			eventID := historyEvents[i].GetEventId()
			if actionIDs != nil {
				eventID = actionIDs[i]
			}
			result.recordFailure(eventID, err)
			continue
		}
	}
	return result
}

func (o *orchestrator) callStateMessage(ctx context.Context, m proto.Message, historyEvent *backend.HistoryEvent, target string, method string) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	actorType := o.actorType

	if historyEvent != nil && historyEvent.GetRouter() != nil {
		router := historyEvent.GetRouter()
		log.Debugf("Cross-app child workflow call: target appID=%s, source appID=%s", router.GetTargetAppID(), router.GetSourceAppID())

		switch m := m.(type) {
		case *backend.CreateWorkflowInstanceRequest:
			if router.TargetAppID != nil {
				actorType = o.actorTypeBuilder.Workflow(router.GetTargetAppID())
			}
		case *backend.HistoryEvent:
			var routeAppID string
			if m.GetChildWorkflowInstanceCompleted() != nil || m.GetChildWorkflowInstanceFailed() != nil {
				if router.TargetAppID == nil {
					return errors.New("child workflow completion events should have a target appID")
				}
				routeAppID = router.GetTargetAppID()
			} else {
				routeAppID = router.GetSourceAppID()
			}

			if routeAppID != "" && routeAppID != o.appID {
				actorType = o.actorTypeBuilder.Workflow(routeAppID)
			}
		}
	}

	log.Debugf("Workflow actor '%s': invoking method '%s' on workflow actor '%s||%s'", o.actorID, method, actorType, target)

	callCtx, cancel := context.WithTimeout(ctx, dispatchTimeout)
	defer cancel()

	if _, err = o.router.Call(callCtx, internalsv1pb.
		NewInternalInvokeRequest(method).
		WithActor(actorType, target).
		WithData(b).
		WithContentType(invokev1.ProtobufContentType),
	); err != nil {
		// If the call was denied by a workflow access policy, fail the child
		// orchestration immediately rather than retrying. Only do this when
		// we can correlate the failure to a parent task via ParentInstance.
		if isPermissionDenied(err) && historyEvent != nil {
			if es := historyEvent.GetExecutionStarted(); es != nil {
				if es.GetParentInstance() != nil {
					if fErr := o.failChildWorkflowACL(ctx, es.GetParentInstance().GetTaskScheduledId(), err); fErr != nil {
						return fmt.Errorf("failed to record child workflow failure: %w (original: %v)", fErr, err)
					}
					return nil
				}
				// Detached spawn: the caller has already returned the
				// instance ID synchronously from ScheduleNewWorkflow, so
				// there is no awaitable Task to fail. Surface the denial
				// via a typed error so run.go can fail the parent
				// terminally — the spawn never happened, and silently
				// dropping it would hide the policy violation.
				//
				// In practice the router has a target app ID here (a
				// permission-denied error only comes back from a cross-app
				// invocation), but extract it defensively so the diagnostic
				// stays accurate if a future caller reaches this branch
				// without one.
				targetAppID := o.appID
				if r := historyEvent.GetRouter(); r != nil && r.GetTargetAppID() != "" {
					targetAppID = r.GetTargetAppID()
				}
				return &detachedSpawnDeniedError{
					instanceID:  target,
					targetAppID: targetAppID,
					cause:       err,
				}
			}
		}

		if router := historyEvent.GetRouter(); router != nil && router.TargetAppID != nil {
			return fmt.Errorf("failed to invoke '%s' on remote app '%s' (the app may not be available): %w", method, router.GetTargetAppID(), err)
		}

		return fmt.Errorf("failed to invoke method '%s' on actor '%s': %w", method, target, err)
	}

	return nil
}
