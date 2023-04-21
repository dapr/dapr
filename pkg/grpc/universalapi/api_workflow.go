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

package universalapi

import (
	"context"
	"unicode"

	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *UniversalAPI) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if err := a.validateInstanceId(in.InstanceId, false /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	req := workflows.GetRequest{
		InstanceID: in.InstanceId,
	}
	response, err := workflowComponent.Get(ctx, &req)
	if err != nil {
		err := messages.ErrWorkflowGetResponse.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
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

func (a *UniversalAPI) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	if err := a.validateInstanceId(in.InstanceId, true /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	if in.WorkflowName == "" {
		err := messages.ErrWorkflowNameMissing
		a.Logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	req := workflows.StartRequest{
		InstanceID:    in.InstanceId,
		Options:       in.Options,
		WorkflowName:  in.WorkflowName,
		WorkflowInput: in.Input,
	}

	resp, err := workflowComponent.Start(ctx, &req)
	if err != nil {
		err := messages.ErrStartWorkflow.WithFormat(in.WorkflowName, err)
		a.Logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}
	ret := &runtimev1pb.StartWorkflowResponse{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

// TerminateWorkflowAlpha1 is the API handler for terminating a workflow
func (a *UniversalAPI) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceId(in.InstanceId, false /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.TerminateRequest{
		InstanceID: in.InstanceId,
	}
	if err := workflowComponent.Terminate(ctx, req); err != nil {
		err = messages.ErrTerminateWorkflow.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

func (a *UniversalAPI) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceId(in.InstanceId, false /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	if in.EventName == "" {
		err := messages.ErrMissingWorkflowEventName
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	req := workflows.RaiseEventRequest{
		InstanceID: in.InstanceId,
		EventName:  in.EventName,
		EventData:  in.Input,
	}

	err = workflowComponent.RaiseEvent(ctx, &req)
	if err != nil {
		err = messages.ErrRaiseEventWorkflow.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// PauseWorkflowAlpha1 is the API handler for pausing a workflow
func (a *UniversalAPI) PauseWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceId(in.InstanceId, false /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.PauseRequest{
		InstanceID: in.InstanceId,
	}
	if err := workflowComponent.Pause(ctx, req); err != nil {
		err = messages.ErrPauseWorkflow.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// ResumeWorkflowAlpha1 is the API handler for resuming a workflow
func (a *UniversalAPI) ResumeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceId(in.InstanceId, false /* isCreate */); err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	workflowComponent, err := a.getComponent(in.WorkflowComponent)
	if err != nil {
		a.Logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.ResumeRequest{
		InstanceID: in.InstanceId,
	}
	if err := workflowComponent.Resume(ctx, req); err != nil {
		err = messages.ErrResumeWorkflow.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

func (a *UniversalAPI) validateInstanceId(instanceID string, isCreate bool) error {
	if instanceID == "" {
		return messages.ErrMissingOrEmptyInstance
	}

	if isCreate {
		// Limit the length of the instance ID to avoid potential conflicts with state stores that have restrictive key limits.
		const maxInstanceIdLength = 64
		if len(instanceID) > maxInstanceIdLength {
			return messages.ErrInstanceIDTooLong.WithFormat(maxInstanceIdLength)
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

func (a *UniversalAPI) getComponent(componentName string) (workflows.Workflow, error) {
	if componentName == "" {
		return nil, messages.ErrNoOrMissingWorkflowComponent
	}

	workflowComponent, ok := a.CompStore.GetWorkflow(componentName)
	if !ok {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(componentName)
		a.Logger.Debug(err)
		return nil, err
	}
	return workflowComponent, nil
}
