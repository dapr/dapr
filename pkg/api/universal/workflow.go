/*
Copyright 2023 The Dapr Authors
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

package universal

import (
	"context"
	"errors"
	"unicode"

	"github.com/google/uuid"
	"github.com/microsoft/durabletask-go/api"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// GetWorkflowBeta1 is the API handler for getting workflow details
func (a *Universal) GetWorkflowBeta1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	req := workflows.GetRequest{
		InstanceID: in.GetInstanceId(),
	}
	response, err := workflowComponent.Get(ctx, &req)
	if err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = messages.ErrWorkflowInstanceNotFound.WithFormat(in.GetInstanceId(), err)
		} else {
			err = messages.ErrWorkflowGetResponse.WithFormat(in.GetInstanceId(), err)
		}
		a.logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	res := &runtimev1pb.GetWorkflowResponse{
		InstanceId:    response.Workflow.InstanceID,
		WorkflowName:  response.Workflow.WorkflowName,
		CreatedAt:     timestamppb.New(response.Workflow.CreatedAt),
		LastUpdatedAt: timestamppb.New(response.Workflow.LastUpdatedAt),
		RuntimeStatus: response.Workflow.RuntimeStatus,
		Properties:    response.Workflow.Properties,
	}
	return res, nil
}

// StartWorkflowBeta1 is the API handler for starting a workflow
func (a *Universal) StartWorkflowBeta1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	// The instance ID is optional. If not specified, we generate a random one.
	if in.GetInstanceId() == "" {
		randomID, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		in.InstanceId = randomID.String()
	}
	if err := a.validateInstanceID(in.GetInstanceId(), true /* isCreate */); err != nil {
		a.logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	if in.GetWorkflowName() == "" {
		err := messages.ErrWorkflowNameMissing
		a.logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	req := workflows.StartRequest{
		InstanceID:    in.GetInstanceId(),
		Options:       in.GetOptions(),
		WorkflowName:  in.GetWorkflowName(),
		WorkflowInput: in.GetInput(),
	}

	resp, err := workflowComponent.Start(ctx, &req)
	if err != nil {
		err := messages.ErrStartWorkflow.WithFormat(in.GetWorkflowName(), err)
		a.logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}
	ret := &runtimev1pb.StartWorkflowResponse{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

// TerminateWorkflowBeta1 is the API handler for terminating a workflow
func (a *Universal) TerminateWorkflowBeta1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.TerminateRequest{
		InstanceID: in.GetInstanceId(),
		Recursive:  true,
	}
	if err := workflowComponent.Terminate(ctx, req); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = messages.ErrWorkflowInstanceNotFound.WithFormat(in.GetInstanceId(), err)
		} else {
			err = messages.ErrTerminateWorkflow.WithFormat(in.GetInstanceId(), err)
		}
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// RaiseEventWorkflowBeta1 is the API handler for raising an event to a workflow
func (a *Universal) RaiseEventWorkflowBeta1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	if in.GetEventName() == "" {
		err := messages.ErrMissingWorkflowEventName
		a.logger.Debug(err)
		return emptyResponse, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := workflows.RaiseEventRequest{
		InstanceID: in.GetInstanceId(),
		EventName:  in.GetEventName(),
		EventData:  in.GetEventData(),
	}

	err = workflowComponent.RaiseEvent(ctx, &req)
	if err != nil {
		err = messages.ErrRaiseEventWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// PauseWorkflowBeta1 is the API handler for pausing a workflow
func (a *Universal) PauseWorkflowBeta1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.PauseRequest{
		InstanceID: in.GetInstanceId(),
	}
	if err := workflowComponent.Pause(ctx, req); err != nil {
		err = messages.ErrPauseWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// ResumeWorkflowBeta1 is the API handler for resuming a workflow
func (a *Universal) ResumeWorkflowBeta1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.ResumeRequest{
		InstanceID: in.GetInstanceId(),
	}
	if err := workflowComponent.Resume(ctx, req); err != nil {
		err = messages.ErrResumeWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// PurgeWorkflowBeta1 is the API handler for purging a workflow
func (a *Universal) PurgeWorkflowBeta1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	a.workflowEngine.WaitForWorkflowEngineReady(ctx)

	workflowComponent, err := a.getWorkflowComponent(in.GetWorkflowComponent())
	if err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := workflows.PurgeRequest{
		InstanceID: in.GetInstanceId(),
		Recursive:  true,
	}

	err = workflowComponent.Purge(ctx, &req)
	if err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = messages.ErrWorkflowInstanceNotFound.WithFormat(in.GetInstanceId(), err)
		} else {
			err = messages.ErrPurgeWorkflow.WithFormat(in.GetInstanceId(), err)
		}
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// GetWorkflowAlpha1 is the API handler for getting workflow details
func (a *Universal) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	return a.GetWorkflowBeta1(ctx, in)
}

// StartWorkflowAlpha1 is the API handler for starting a workflow
func (a *Universal) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	return a.StartWorkflowBeta1(ctx, in)
}

// TerminateWorkflowAlpha1 is the API handler for terminating a workflow
func (a *Universal) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	return a.TerminateWorkflowBeta1(ctx, in)
}

// RaiseEventWorkflowAlpha1 is the API handler for raising an event to a workflow
func (a *Universal) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	return a.RaiseEventWorkflowBeta1(ctx, in)
}

// PauseWorkflowAlpha1 is the API handler for pausing a workflow
func (a *Universal) PauseWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	return a.PauseWorkflowBeta1(ctx, in)
}

// ResumeWorkflowAlpha1 is the API handler for resuming a workflow
func (a *Universal) ResumeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	return a.ResumeWorkflowBeta1(ctx, in)
}

// PurgeWorkflowAlpha1 is the API handler for purging a workflow
func (a *Universal) PurgeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	return a.PurgeWorkflowBeta1(ctx, in)
}

func (a *Universal) validateInstanceID(instanceID string, isCreate bool) error {
	if instanceID == "" {
		return messages.ErrMissingOrEmptyInstance
	}

	if isCreate {
		// Limit the length of the instance ID to avoid potential conflicts with state stores that have restrictive key limits.
		const maxInstanceIDLength = 64
		if len(instanceID) > maxInstanceIDLength {
			return messages.ErrInstanceIDTooLong.WithFormat(maxInstanceIDLength)
		}

		// Check to see if the instance ID contains invalid characters. Valid characters are letters, digits, dashes, and underscores.
		// See https://github.com/dapr/dapr/issues/6156 for more context on why we check this.
		for _, c := range instanceID {
			if !unicode.IsLetter(c) && c != '_' && c != '-' && !unicode.IsDigit(c) {
				return messages.ErrInvalidInstanceID.WithFormat(instanceID)
			}
		}
	}
	return nil
}

func (a *Universal) getWorkflowComponent(componentName string) (workflows.Workflow, error) {
	if componentName == "" {
		return nil, messages.ErrNoOrMissingWorkflowComponent
	}

	workflowComponent, ok := a.compStore.GetWorkflow(componentName)
	if !ok {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(componentName)
		a.logger.Debug(err)
		return nil, err
	}
	return workflowComponent, nil
}
