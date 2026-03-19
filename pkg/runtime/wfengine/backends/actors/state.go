/*
Copyright 2026 The Dapr Authors
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

package actors

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/pkg/runtime/wfengine/state"
	"github.com/dapr/dapr/pkg/runtime/wfengine/state/list"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
)

func (abe *Actors) loadInternalState(ctx context.Context, id api.InstanceID) (*state.State, error) {
	astate, err := abe.actors.State(ctx)
	if err != nil {
		return nil, err
	}

	// actor id is workflow instance id
	state, err := state.LoadWorkflowState(ctx, astate, string(id), state.Options{
		AppID:             abe.appID,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		Signer:            abe.signer,
	})
	if err != nil {
		return state, err
	}

	if state == nil {
		// No such state exists in the state store
		return nil, nil
	}

	return state, nil
}

// GetOrchestrationMetadata implements backend.Backend
func (abe *Actors) GetOrchestrationMetadata(ctx context.Context, id api.InstanceID) (*backend.OrchestrationMetadata, error) {
	wfState, err := abe.loadInternalState(ctx, id)
	if err != nil {
		var verifyErr *state.VerificationError
		if errors.As(err, &verifyErr) && wfState != nil {
			return abe.failedMetadataFromState(string(id), wfState, verifyErr), nil
		}
		return nil, err
	}

	if wfState == nil {
		return nil, api.ErrInstanceNotFound
	}

	rstate := runtimestate.NewOrchestrationRuntimeState(string(id), wfState.CustomStatus, wfState.History)

	name, _ := runtimestate.Name(rstate)
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)
	input, _ := runtimestate.Input(rstate)
	output, _ := runtimestate.Output(rstate)
	failureDetuils, _ := runtimestate.FailureDetails(rstate)

	return &backend.OrchestrationMetadata{
		InstanceId:     string(id),
		Name:           name,
		RuntimeStatus:  runtimestate.RuntimeStatus(rstate),
		CreatedAt:      timestamppb.New(createdAt),
		LastUpdatedAt:  timestamppb.New(lastUpdated),
		Input:          input,
		Output:         output,
		CustomStatus:   rstate.GetCustomStatus(),
		FailureDetails: failureDetuils,
	}, nil
}

func (abe *Actors) failedMetadataFromState(id string, wfState *state.State, verifyErr *state.VerificationError) *backend.OrchestrationMetadata {
	rstate := runtimestate.NewOrchestrationRuntimeState(id, wfState.CustomStatus, wfState.History)

	name, _ := runtimestate.Name(rstate)
	createdAt, _ := runtimestate.CreatedTime(rstate)
	lastUpdated, _ := runtimestate.LastUpdatedTime(rstate)

	return &backend.OrchestrationMetadata{
		InstanceId:    id,
		Name:          name,
		RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
		CreatedAt:     timestamppb.New(createdAt),
		LastUpdatedAt: timestamppb.New(lastUpdated),
		CustomStatus:  rstate.GetCustomStatus(),
		FailureDetails: &protos.TaskFailureDetails{
			ErrorType:      "SignatureVerificationFailed",
			ErrorMessage:   verifyErr.Error(),
			IsNonRetriable: true,
		},
	}
}

// GetOrchestrationRuntimeState implements backend.Backend
func (abe *Actors) GetOrchestrationRuntimeState(ctx context.Context, owi *backend.OrchestrationWorkItem) (*backend.OrchestrationRuntimeState, error) {
	state, err := abe.loadInternalState(ctx, owi.InstanceID)
	if err != nil {
		return nil, err
	}

	if state == nil {
		return nil, api.ErrInstanceNotFound
	}

	runtimeState := runtimestate.NewOrchestrationRuntimeState(string(owi.InstanceID), state.CustomStatus, state.History)

	return runtimeState, nil
}

func (abe *Actors) GetInstanceHistory(ctx context.Context, req *protos.GetInstanceHistoryRequest) (*protos.GetInstanceHistoryResponse, error) {
	ss, err := abe.actors.State(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := state.LoadWorkflowState(ctx, ss, req.GetInstanceId(), state.Options{
		AppID:             abe.appID,
		WorkflowActorType: abe.workflowActorType,
		ActivityActorType: abe.activityActorType,
		Signer:            abe.signer,
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, status.Errorf(codes.NotFound, "workflow instance '%s' not found", req.GetInstanceId())
	}

	return &protos.GetInstanceHistoryResponse{Events: resp.History}, nil
}

func (abe *Actors) ListInstanceIDs(ctx context.Context, req *protos.ListInstanceIDsRequest) (*protos.ListInstanceIDsResponse, error) {
	resp, err := list.ListInstanceIDs(ctx, list.ListOptions{
		ComponentStore:    abe.compStore,
		Namespace:         abe.namespace,
		AppID:             abe.appID,
		PageSize:          req.PageSize,
		ContinuationToken: req.ContinuationToken,
	})
	if err != nil {
		return nil, err
	}

	return &protos.ListInstanceIDsResponse{
		InstanceIds:       resp.Keys,
		ContinuationToken: resp.ContinuationToken,
	}, nil
}
