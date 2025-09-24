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

func (o *orchestrator) callCreateWorkflowStateMessage(ctx context.Context, events []*backend.OrchestrationRuntimeStateMessage) error {
	msgs := make([]proto.Message, len(events))
	historyEvents := make([]*backend.HistoryEvent, len(events))
	targets := make([]string, len(events))

	for i, msg := range events {
		msgs[i] = &backend.CreateWorkflowInstanceRequest{StartEvent: msg.GetHistoryEvent()}
		historyEvents[i] = msg.GetHistoryEvent()
		targets[i] = msg.GetTargetInstanceID()
	}

	return o.callStateMessages(ctx, msgs, historyEvents, targets, todo.CreateWorkflowInstanceMethod)
}

func (o *orchestrator) callAddEventStateMessage(ctx context.Context, events []*backend.OrchestrationRuntimeStateMessage) error {
	msgs := make([]proto.Message, len(events))
	historyEvents := make([]*backend.HistoryEvent, len(events))
	targets := make([]string, len(events))

	for i, msg := range events {
		msgs[i] = msg.GetHistoryEvent()
		historyEvents[i] = msg.GetHistoryEvent()
		targets[i] = msg.GetTargetInstanceID()
	}

	return o.callStateMessages(ctx, msgs, historyEvents, targets, todo.AddWorkflowEventMethod)
}

func (o *orchestrator) callStateMessages(ctx context.Context, msgs []proto.Message, historyEvents []*backend.HistoryEvent, targets []string, method string) error {
	for i, msg := range msgs {
		if err := o.callStateMessage(ctx, msg, historyEvents[i], targets[i], method); err != nil {
			return err
		}
	}

	return nil
}

func (o *orchestrator) callStateMessage(ctx context.Context, m proto.Message, historyEvent *backend.HistoryEvent, target string, method string) error {
	b, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	actorType := o.actorType

	if historyEvent != nil && historyEvent.GetRouter() != nil {
		router := historyEvent.GetRouter()
		log.Debugf("Cross-app suborchestrator call: target appID=%s, source appID=%s", router.GetTargetAppID(), router.GetSourceAppID())

		switch m := m.(type) {
		case *backend.CreateWorkflowInstanceRequest:
			// If target app is specified and different from current app, use cross-app actor type
			if router.TargetAppID != nil {
				actorType = o.actorTypeBuilder.Workflow(router.GetTargetAppID())
			}
		case *backend.HistoryEvent:
			var routeAppID string
			if m.GetSubOrchestrationInstanceCompleted() != nil || m.GetSubOrchestrationInstanceFailed() != nil {
				if router.TargetAppID == nil {
					return errors.New("sub-orchestrator completion events should have a target appID")
				}
				routeAppID = router.GetTargetAppID()
			} else {
				routeAppID = router.GetSourceAppID()
			}

			// If route app is specified and different from current app, use cross-app actor type
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
		return fmt.Errorf("failed to invoke method '%s' on actor '%s': %w", method, target, err)
	}

	return nil
}
