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
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) callChildWorkflows(ctx context.Context, startEventName string, es []*protos.HistoryEvent, outgoingHistory map[int32]*protos.PropagatedHistory) error {
	log.Debugf("Workflow actor '%s': calling %d child workflows", o.actorID, len(es))

	for _, e := range es {
		createSO := e.GetChildWorkflowInstanceCreated()

		//nolint:protogetter
		startEvent := &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.New(time.Now()),
			Router:    e.Router,
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: createSO.Name,
					ParentInstance: &protos.ParentInstanceInfo{
						TaskScheduledId:  e.EventId,
						Name:             wrapperspb.String(startEventName),
						WorkflowInstance: &protos.WorkflowInstance{InstanceId: o.actorID},
						AppID:            new(o.appID),
					},
					Input: createSO.Input,
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId:  createSO.InstanceId,
						ExecutionId: wrapperspb.String(uuid.New().String()),
					},
					ParentTraceContext: createSO.ParentTraceContext,
				},
			},
		}

		createReq := &backend.CreateWorkflowInstanceRequest{
			StartEvent: startEvent,
		}
		if ph := outgoingHistory[e.GetEventId()]; ph != nil {
			createReq.PropagatedHistory = ph
		}

		reqP, err := proto.Marshal(createReq)
		if err != nil {
			return fmt.Errorf("failed to marshal child workflow request: %w", err)
		}

		id := e.GetChildWorkflowInstanceCreated().GetInstanceId()
		req := internalsv1pb.NewInternalInvokeRequest(todo.CreateWorkflowInstanceMethod).
			WithActor(o.actorType, id).
			WithData(reqP).
			WithContentType(invokev1.ProtobufContentType)

		_, err = o.router.Call(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to call child workflow '%s': %w", id, err)
		}
	}

	return nil
}
