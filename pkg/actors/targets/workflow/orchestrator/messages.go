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

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

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

	if _, err = o.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest(method).
		WithActor(actorType, target).
		WithData(b).
		WithContentType(invokev1.ProtobufContentType),
	); err != nil {
		// If the call was denied by a workflow access policy or the target
		// instance ID is already taken by another workflow, fail the child
		// orchestration immediately rather than retrying. Only do this when
		// we can correlate the failure to a parent task via ParentInstance.
		permissionDenied := isPermissionDenied(err)
		if (permissionDenied || isAlreadyExists(err)) && historyEvent != nil {
			if es := historyEvent.GetExecutionStarted(); es != nil {
				errorType := errorTypeAlreadyExists
				errorMessage := status.Convert(err).Message()
				if permissionDenied {
					errorType = errorTypeAccessPolicyDenied
					errorMessage = errorMessageAccessPolicyDenied
				}

				if es.GetParentInstance() != nil {
					log.Warnf("Workflow actor '%s': failing child workflow task for '%s': %v", o.actorID, target, err)
					if fErr := o.failChildWorkflowTask(ctx, es.GetParentInstance().GetTaskScheduledId(), errorType, errorMessage); fErr != nil {
						return fmt.Errorf("failed to record child workflow failure: %w (original: %v)", fErr, err)
					}
					return nil
				}
				// Detached spawn: fire-and-forget by design. There is no
				// awaitable Task on the caller to fail, and propagating
				// the failure back would defeat the decoupling the feature
				// is built on. Log so the rejection is visible in
				// operator output and drop the dispatch attempt: the
				// caller's history records DetachedWorkflowInstanceCreated
				// as audit, the spawn just never lands on the target.
				targetAppID := ""
				if r := historyEvent.GetRouter(); r != nil {
					targetAppID = r.GetTargetAppID()
				}
				log.Warnf("Workflow actor '%s': detached workflow spawn '%s' rejected on target app '%s': %v", o.actorID, target, targetAppID, err)
				return nil
			}
		}

		if router := historyEvent.GetRouter(); router != nil && router.TargetAppID != nil {
			return fmt.Errorf("failed to invoke '%s' on remote app '%s' (the app may not be available): %w", method, router.GetTargetAppID(), err)
		}

		return fmt.Errorf("failed to invoke method '%s' on actor '%s': %w", method, target, err)
	}

	return nil
}
