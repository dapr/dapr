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
	"net/http"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	actorsapi "github.com/dapr/dapr/pkg/actors/api"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	wfenginestate "github.com/dapr/dapr/pkg/runtime/wfengine/state"
	wferrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/concurrency"
)

func (o *orchestrator) loadInternalState(ctx context.Context) (*wfenginestate.State, *backend.WorkflowMetadata, error) {
	// See if the state for this actor is already cached in memory
	if o.state != nil {
		return o.state, o.ometa, nil
	}

	// state is not cached, so try to load it from the state store
	state, err := wfenginestate.LoadWorkflowState(ctx, o.actorState, o.actorID, wfenginestate.Options{
		AppID:             o.appID,
		WorkflowActorType: o.actorType,
		ActivityActorType: o.activityActorType,
		Signer:            o.signer,
	})
	if err != nil {
		var verifyErr *wferrors.VerificationError
		if errors.As(err, &verifyErr) {
			o.failSignatureVerification(ctx)
		}
		return nil, nil, err
	}
	if state == nil {
		// No such state exists in the state store
		return nil, nil, nil
	}

	// Update cached state
	o.state = state
	o.rstate = runtimestate.NewWorkflowRuntimeState(o.actorID, state.CustomStatus, state.History)
	o.ometa = o.ometaFromState(o.rstate, o.getExecutionStartedEvent(state))

	return state, o.ometa, nil
}

// signAndSaveState signs any newly added history events and then persists
// the state. This is the single entry point for all state persistence —
// callers must never call saveInternalState directly.
func (o *orchestrator) signAndSaveState(ctx context.Context, state *wfenginestate.State) error {
	if err := o.signNewEvents(state, state.HistoryAddedCount()); err != nil {
		return fmt.Errorf("failed to sign new history events: %w", err)
	}
	return o.saveInternalState(ctx, state)
}

func (o *orchestrator) saveInternalState(ctx context.Context, state *wfenginestate.State) error {
	// generate and run a state store operation that saves all changes
	req, err := state.GetSaveRequest(o.actorID)
	if err != nil {
		return err
	}

	log.Debugf("Workflow actor '%s': saving %d keys to actor state store", o.actorID, len(req.Operations))

	if err = o.actorState.TransactionalStateOperation(ctx, true, req, false); err != nil {
		return err
	}

	// ResetChangeTracking should always be called after a save operation succeeds
	state.ResetChangeTracking()

	// Update cached state
	o.state = state
	o.rstate = runtimestate.NewWorkflowRuntimeState(o.actorID, state.CustomStatus, state.History)
	o.ometa = o.ometaFromState(o.rstate, o.getExecutionStartedEvent(state))
	if o.eventSink != nil {
		o.eventSink(o.ometa)
	}

	if len(o.streamFns) > 0 {
		arstate, err := anypb.New(o.ometa)
		if err != nil {
			return err
		}

		streamReq := &internalsv1pb.InternalInvokeResponse{
			Status:  &internalsv1pb.Status{Code: http.StatusOK},
			Message: &commonv1pb.InvokeResponse{Data: arstate},
		}

		var ok bool
		for idx, stream := range o.streamFns {
			if stream.done.Load() {
				delete(o.streamFns, idx)
				continue
			}

			ok, err = stream.fn(streamReq)
			if err != nil || ok {
				stream.errCh <- err
				delete(o.streamFns, idx)
			}
		}
	}

	return nil
}

// This method cleans up a workflow associated with the given actorID
func (o *orchestrator) cleanupWorkflowStateInternal(ctx context.Context, state *wfenginestate.State, includeRetentionReminder bool) error {
	// This will create a request to purge everything.
	req, err := state.GetPurgeRequest(o.actorID)
	if err != nil {
		return err
	}

	runners := []concurrency.Runner{
		func(ctx context.Context) error {
			// This will do the purging
			return o.actorState.TransactionalStateOperation(ctx, true, req, false)
		},
		func(ctx context.Context) error {
			return o.reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       o.actorType,
				ActorID:         o.actorID,
				MatchIDAsPrefix: false,
			})
		},
		func(ctx context.Context) error {
			return o.reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       o.activityActorType,
				ActorID:         o.actorID + "::",
				MatchIDAsPrefix: true,
			})
		},
	}

	if includeRetentionReminder {
		runners = append(runners, func(ctx context.Context) error {
			return o.reminders.DeleteByActorID(ctx, &actorsapi.DeleteRemindersByActorIDRequest{
				ActorType:       o.retentionActorType,
				ActorID:         o.actorID,
				MatchIDAsPrefix: false,
			})
		})
	}

	if err = concurrency.Join(ctx, runners...); err != nil {
		return err
	}

	o.deactivate(o)

	return nil
}

func (o *orchestrator) ometaFromState(rstate *backend.WorkflowRuntimeState, startEvent *protos.ExecutionStartedEvent) *backend.WorkflowMetadata {
	var se *protos.ExecutionStartedEvent = nil
	if rstate.GetStartEvent() != nil {
		se = rstate.GetStartEvent()
	} else if startEvent != nil {
		se = startEvent
	}

	name, _ := runtimestate.Name(rstate)
	if name == "" && se != nil {
		name = se.GetName()
	}
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)
	completedAt, _ := runtimestate.CompletedTime(rstate)
	input, _ := runtimestate.Input(rstate)
	output, _ := runtimestate.Output(rstate)
	failureDetails, _ := runtimestate.FailureDetails(rstate)
	var parentInstanceID string
	if se != nil && se.GetParentInstance() != nil && se.GetParentInstance().GetWorkflowInstance() != nil {
		parentInstanceID = se.GetParentInstance().GetWorkflowInstance().GetInstanceId()
	}
	return &backend.WorkflowMetadata{
		InstanceId:       rstate.GetInstanceId(),
		Name:             name,
		RuntimeStatus:    runtimestate.RuntimeStatus(rstate),
		CreatedAt:        timestamppb.New(createdAt),
		LastUpdatedAt:    timestamppb.New(lastUpdated),
		CompletedAt:      timestamppb.New(completedAt),
		Input:            input,
		Output:           output,
		CustomStatus:     rstate.GetCustomStatus(),
		FailureDetails:   failureDetails,
		ParentInstanceId: parentInstanceID,
	}
}

// This method purges all the completed activity data from a workflow associated with the given actorID
func (o *orchestrator) purgeWorkflowState(ctx context.Context, meta map[string]*internalsv1pb.ListStringValue) error {
	defer o.deactivate(o)

	log.Debugf("Workflow actor '%s': purging workflow state", o.actorID)

	state, _, err := o.loadInternalState(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		return api.ErrInstanceNotFound
	}

	if o.rstate.Stalled != nil {
		return api.ErrStalled
	}

	// If the workflow is required to complete but it's not yet completed then
	// return [ErrNotCompleted] This check is used by purging workflow
	if !runtimestate.IsCompleted(o.rstate) {
		return api.ErrNotCompleted
	}

	s, ok := meta[todo.MetadataPurgeRetentionCall]
	retentionCall := ok && len(s.GetValues()) > 0 && s.GetValues()[0] == "true"

	return o.cleanupWorkflowStateInternal(ctx, state, !retentionCall)
}

func (o *orchestrator) getExecutionStartedEvent(state *wfenginestate.State) *protos.ExecutionStartedEvent {
	for _, e := range state.History {
		if es := e.GetExecutionStarted(); es != nil {
			return es
		}
	}
	for _, e := range state.Inbox {
		if es := e.GetExecutionStarted(); es != nil {
			return es
		}
	}
	return &protos.ExecutionStartedEvent{}
}
