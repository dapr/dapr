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

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callCreateWorkflowStateMessage(ctx context.Context, events []*backend.WorkflowRuntimeStateMessage) dispatchResult {
	msgs := make([]proto.Message, len(events))
	historyEvents := make([]*backend.HistoryEvent, len(events))
	targets := make([]string, len(events))
	actionIDs := make([]int32, len(events))

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
		if es := msg.GetHistoryEvent().GetExecutionStarted(); es != nil && es.GetParentInstance() != nil {
			actionIDs[i] = es.GetParentInstance().GetTaskScheduledId()
		} else {
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
		if router := historyEvent.GetRouter(); router != nil && router.TargetAppID != nil {
			return fmt.Errorf("failed to invoke '%s' on remote app '%s' (the app may not be available): %w", method, router.GetTargetAppID(), err)
		}
		return fmt.Errorf("failed to invoke '%s' on actor '%s': %w", method, target, err)
	}

	return nil
}
