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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/orchestrator/fork"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (o *orchestrator) forkWorkflowHistory(ctx context.Context, request []byte) error {
	var rerunReq backend.RerunWorkflowFromEventRequest
	if err := proto.Unmarshal(request, &rerunReq); err != nil {
		return fmt.Errorf("failed to unmarshal rerun workflow request: %w", err)
	}

	state, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state == nil {
		defer o.factory.deactivate(o)
		return status.Errorf(codes.NotFound, "workflow instance does not exist with ID '%s'", o.actorID)
	}

	if !api.OrchestrationMetadataIsComplete(ometa) {
		return status.Errorf(codes.InvalidArgument, "'%s' is not in a terminal state", o.actorID)
	}

	defer o.factory.deactivate(o)

	fork := fork.New(fork.Options{
		AppID:             o.appID,
		ActorType:         o.actorType,
		ActivityActorType: o.activityActorType,
		//nolint:gosec
		TargetEventID: int32(rerunReq.GetEventID()),

		OverwriteInput: rerunReq.GetOverwriteInput(),
		Input:          rerunReq.Input,
		OldState:       state,
	})

	newState, err := fork.Build()
	if err != nil {
		return err
	}

	data, err := proto.Marshal(newState.ToWorkflowState())
	if err != nil {
		return err
	}

	// Call target instance ID to execute workflow rerun.
	_, err = o.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest(todo.RerunWorkflowInstance).
		WithActor(o.actorType, rerunReq.GetNewInstanceID()).
		WithData(data).
		WithContentType(invokev1.ProtobufContentType),
	)

	return err
}

func (o *orchestrator) rerunWorkflowInstanceRequest(ctx context.Context, request []byte) error {
	state, ometa, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state != nil || ometa != nil {
		return status.Errorf(codes.AlreadyExists, "workflow '%s' has already been created", o.actorID)
	}

	var workflowState backend.WorkflowState
	if err = proto.Unmarshal(request, &workflowState); err != nil {
		return fmt.Errorf("failed to unmarshal workflow history: %w", err)
	}

	if len(workflowState.Inbox) == 0 {
		return errors.New("expect rerun workflow inbox to not be empty")
	}

	var activities []*protos.HistoryEvent
	var timers []*protos.HistoryEvent

	for i := 0; i < len(workflowState.Inbox); i++ {
		his := workflowState.Inbox[i]
		his.Timestamp = timestamppb.Now()

		switch his.GetEventType().(type) {
		case *protos.HistoryEvent_TaskScheduled:
			activities = append(activities, his)

		case *protos.HistoryEvent_TimerCreated:
			timers = append(timers, &protos.HistoryEvent{
				EventId:   -1,
				Timestamp: timestamppb.New(time.Now()),
				EventType: &protos.HistoryEvent_TimerFired{
					TimerFired: &protos.TimerFiredEvent{
						TimerId: his.GetEventId(),
						FireAt:  his.GetTimerCreated().GetFireAt(),
					},
				},
			})

		default:
			return status.Errorf(codes.InvalidArgument, "unexpected event type '%T' in inbox", his.GetEventType())
		}

		workflowState.History = append(workflowState.History, his)
		workflowState.Inbox = append(workflowState.Inbox[:i], workflowState.Inbox[i+1:]...)
		i--
	}

	newState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             o.appID,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
	})

	newState.FromWorkflowState(&workflowState)

	if err = o.saveInternalState(ctx, newState); err != nil {
		return fmt.Errorf("failed to save workflow state: %w", err)
	}

	if err = errors.Join(
		o.callActivities(ctx, activities, newState.Generation),
		o.createTimers(ctx, timers, newState.Generation),
	); err != nil {
		return err
	}

	return nil
}
