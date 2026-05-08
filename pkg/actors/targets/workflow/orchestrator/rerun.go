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
	"slices"
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
	"github.com/dapr/durabletask-go/backend/runtimestate"
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
		defer o.deactivate(o)
		return status.Errorf(codes.NotFound, "workflow instance does not exist with ID '%s'", o.actorID)
	}

	if !api.WorkflowMetadataIsComplete(ometa) {
		return status.Errorf(codes.InvalidArgument, "'%s' is not in a terminal state", o.actorID)
	}

	if len(ometa.ParentInstanceId) > 0 {
		return status.Errorf(codes.InvalidArgument, "'%s': cannot rerun from child-workflows", o.actorID)
	}

	defer o.deactivate(o)

	fork := fork.New(fork.Options{
		InstanceID:                 o.actorID,
		NewInstanceID:              rerunReq.GetNewInstanceID(),
		NewChildWorkflowInstanceID: rerunReq.NewChildWorkflowInstanceID,
		AppID:                      o.appID,
		Namespace:                  o.namespace,
		ActorType:                  o.actorType,
		ActivityActorType:          o.activityActorType,
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

	var workflowState protos.BackendWorkflowState
	if err = proto.Unmarshal(request, &workflowState); err != nil {
		return fmt.Errorf("failed to unmarshal workflow history: %w", err)
	}

	if len(workflowState.GetInbox()) == 0 {
		return errors.New("expect rerun workflow inbox to not be empty")
	}

	var activities []*protos.HistoryEvent
	var timers []*protos.HistoryEvent
	var childWFs []*protos.HistoryEvent

	for i := 0; i < len(workflowState.GetInbox()); i++ {
		his := workflowState.GetInbox()[i]
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

		case *protos.HistoryEvent_ChildWorkflowInstanceCreated:
			childWFs = append(childWFs, his)

		default:
			return status.Errorf(codes.InvalidArgument, "unexpected event type '%T' in inbox", his.GetEventType())
		}

		workflowState.History = append(workflowState.History, his)
		workflowState.Inbox = slices.Delete(workflowState.GetInbox(), i, i+1)
		i--
	}

	newState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             o.appID,
		Namespace:         o.namespace,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
	})

	newState.FromWorkflowState(&workflowState)

	if err = o.signAndSaveState(ctx, newState); err != nil {
		return fmt.Errorf("failed to save workflow state: %w", err)
	}

	startedEvent := o.getExecutionStartedEvent(newState)

	// Re-driven activities and child workflows preserve the propagation scope
	// they originally had: scope is persisted on TaskScheduledEvent and
	// ChildWorkflowInstanceCreatedEvent so it survives the action being
	// discarded. Non-propagating tasks stay non-propagating & propagating
	// tasks are re-issued with a chunk reflecting the rerunning workflow's
	// current state plus whatever lineage it received from its parent.
	outgoingActPropHist, err := buildRerunOutgoingHistory(activities, newState, o.actorID, o.appID, taskScheduledScope)
	if err != nil {
		return err
	}
	outgoingChildPropHist, err := buildRerunOutgoingHistory(childWFs, newState, o.actorID, o.appID, childWorkflowCreatedScope)
	if err != nil {
		return err
	}

	rerunRS := runtimestate.NewWorkflowRuntimeState(o.actorID, newState.CustomStatus, newState.History)

	// Attach this workflow's signatures + cert chain to the current-app
	// chunk of each outbound PropagatedHistory. Lineage chunks pass
	// through verbatim. signAndSaveState above already populated
	// state.Signatures / state.RawSignatures. No-op if signing is off.
	for _, ph := range outgoingActPropHist {
		if err = o.signing.SignOutgoingPropagatedHistory(ph, o.appID); err != nil {
			return err
		}
	}
	for _, ph := range outgoingChildPropHist {
		if err = o.signing.SignOutgoingPropagatedHistory(ph, o.appID); err != nil {
			return err
		}
	}

	if err = errors.Join(
		o.callChildWorkflows(ctx, startedEvent.GetName(), childWFs, outgoingChildPropHist),
		o.callActivities(ctx, activities, newState, rerunRS, outgoingActPropHist).err,
		o.createTimers(ctx, timers, newState.Generation),
	); err != nil {
		return err
	}

	return nil
}

func taskScheduledScope(e *protos.HistoryEvent) protos.HistoryPropagationScope {
	return e.GetTaskScheduled().GetHistoryPropagationScope()
}

func childWorkflowCreatedScope(e *protos.HistoryEvent) protos.HistoryPropagationScope {
	return e.GetChildWorkflowInstanceCreated().GetHistoryPropagationScope()
}

func buildRerunOutgoingHistory(
	events []*protos.HistoryEvent,
	state *wfenginestate.State,
	instanceID string,
	appID string,
	scopeOf func(*protos.HistoryEvent) protos.HistoryPropagationScope,
) (map[int32]*protos.PropagatedHistory, error) {
	var out map[int32]*protos.PropagatedHistory
	var rt *protos.WorkflowRuntimeState
	for _, e := range events {
		scope := scopeOf(e)
		if scope == protos.HistoryPropagationScope_HISTORY_PROPAGATION_SCOPE_NONE {
			continue
		}
		if rt == nil {
			rt = runtimestate.NewWorkflowRuntimeState(instanceID, nil, state.History)
		}
		chunk, err := runtimestate.AssembleProtoPropagatedHistory(rt, scope, state.IncomingHistory, appID)
		if err != nil {
			return nil, fmt.Errorf("failed to assemble propagated history for event %d: %w", e.GetEventId(), err)
		}
		if chunk == nil {
			continue
		}
		if out == nil {
			out = make(map[int32]*protos.PropagatedHistory, len(events))
		}
		out[e.GetEventId()] = chunk
	}
	return out, nil
}
