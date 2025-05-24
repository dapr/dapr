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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func (w *workflow) forkWorkflowHistory(ctx context.Context, request []byte) error {
	var rerunReq backend.RerunWorkflowFromEventRequest
	if err := proto.Unmarshal(request, &rerunReq); err != nil {
		return fmt.Errorf("failed to unmarshal rerun workflow request: %w", err)
	}

	state, ometa, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state == nil {
		return status.Errorf(codes.NotFound, "workflow instance does not exist with ID '%s'", w.actorID)
	}

	if !api.OrchestrationMetadataIsComplete(ometa) {
		return status.Errorf(codes.InvalidArgument, "'%s' is not in a terminal state", w.actorID)
	}

	defer w.table.DeleteFromTableIn(w, 0)

	newState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             w.appID,
		WorkflowActorType: w.actorType,
		ActivityActorType: w.activityActorType,
	})

	//nolint:gosec
	targetEventID := int32(rerunReq.GetEventID())

	unfinishedActivities := make(map[int32]*backend.HistoryEvent)
	activeTimers := make(map[int32]*backend.HistoryEvent)

	var found bool
	for _, his := range state.History {
		if his.GetEventId() != targetEventID {
			// Track activities which have not been completed yet so they are also
			// rerun.
			switch his.GetEventType().(type) {
			case *protos.HistoryEvent_TaskScheduled:
				unfinishedActivities[his.GetEventId()] = his
			case *protos.HistoryEvent_TaskCompleted:
				delete(unfinishedActivities, his.GetTaskCompleted().GetTaskScheduledId())
			case *protos.HistoryEvent_TimerCreated:
				activeTimers[his.GetEventId()] = his
			case *protos.HistoryEvent_TimerFired:
				delete(activeTimers, his.GetTimerFired().GetTimerId())
			case *protos.HistoryEvent_EventRaised:
				for i, timer := range activeTimers {
					if timer.GetTimerCreated().GetName() == his.GetEventRaised().GetName() {
						delete(activeTimers, i)
						break
					}
				}
			}

			newState.AddToHistory(his)
			continue
		}

		// TODO: @joshvanl: we should support timers, raise signal events, and sub
		// orchestration creation events as re-runnable targets. This involves
		// creating new timer and raise event actors.
		// Do in a future change.
		sched := his.GetTaskScheduled()
		if sched == nil {
			return status.Errorf(codes.NotFound,
				"'%s' target event ID '%d' is not a TaskScheduled event",
				w.actorID, targetEventID)
		}

		if rerunReq.GetOverwriteInput() {
			sched.Input = rerunReq.GetInput()
		}

		newState.AddToInbox(his)
		found = true
		break
	}

	if !found {
		return status.Errorf(codes.NotFound,
			"'%s' does not have history event with ID '%d'",
			w.actorID, rerunReq.GetEventID(),
		)
	}

	// TODO: @joshvanl: We should be able to rerun workflows even if there are
	// active timers. Timers should be re-created with the same due-time as the
	// original workflow instance.
	// To do this, we need to create a new timer actor.
	// Support rerunning active timers in a future change.
	if len(activeTimers) > 0 {
		return status.Errorf(codes.Aborted, "'%s' would have active timers, cannot rerun workflow", w.actorID)
	}

	// Ensure incomplete activities are also rerun.
	for id, unfin := range unfinishedActivities {
		newState.AddToInbox(unfin)

		for j, his := range newState.History {
			if his.GetEventId() == id {
				newState.History = append(newState.History[:j], newState.History[j+1:]...)
				break
			}
		}
	}

	data, err := proto.Marshal(newState.ToWorkflowState())
	if err != nil {
		return err
	}

	// Call target instance ID to execute workflow rerun.
	_, err = w.router.Call(ctx, internalsv1pb.
		NewInternalInvokeRequest(todo.RerunWorkflowInstance).
		WithActor(w.actorType, rerunReq.GetNewInstanceID()).
		WithData(data).
		WithContentType(invokev1.ProtobufContentType),
	)

	return err
}

func (w *workflow) rerunWorkflowInstanceRequest(ctx context.Context, request []byte) error {
	state, ometa, err := w.loadInternalState(ctx)
	if err != nil {
		return err
	}

	if state != nil || ometa != nil {
		return status.Errorf(codes.AlreadyExists, "workflow '%s' has already been created", w.actorID)
	}

	var workflowState backend.WorkflowState
	if err := proto.Unmarshal(request, &workflowState); err != nil {
		return fmt.Errorf("failed to unmarshal workflow history: %w", err)
	}

	newState := wfenginestate.NewState(wfenginestate.Options{
		AppID:             w.appID,
		WorkflowActorType: w.actorType,
		ActivityActorType: w.activityActorType,
	})

	newState.FromWorkflowState(&workflowState)

	if len(newState.Inbox) == 0 {
		return errors.New("expect rerun workflow inbox to not be empty")
	}

	if err := w.saveInternalState(ctx, newState); err != nil {
		return fmt.Errorf("failed to save workflow state: %w", err)
	}

	return w.callActivities(ctx, newState.Inbox, newState.Generation)
}
