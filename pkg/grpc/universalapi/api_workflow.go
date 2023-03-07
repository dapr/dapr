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
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *UniversalAPI) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	workflowRun := a.WorkflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist, in.WorkflowComponent))
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}
	response, err := workflowRun.Get(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrWorkflowGetResponse, err))
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	id := &runtimev1pb.WorkflowReference{
		InstanceId: response.WFInfo.InstanceID,
	}

	t, err := time.Parse(time.RFC3339, response.StartTime)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrTimerParse, err))
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	res := &runtimev1pb.GetWorkflowResponse{
		InstanceId: id.InstanceId,
		StartTime:  t.Unix(),
		Metadata:   response.Metadata,
	}
	return res, nil
}

func (a *UniversalAPI) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.WorkflowReference, error) {
	if in.WorkflowName == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrWorkflowNameMissing)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	workflowRun := a.WorkflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist, in.WorkflowComponent))
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	wf := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}

	var inputMap map[string]interface{}
	json.Unmarshal(in.Input, &inputMap)
	req := workflows.StartRequest{
		WorkflowReference: wf,
		Options:           in.Options,
		WorkflowName:      in.WorkflowName,
		Input:             inputMap,
	}

	resp, err := workflowRun.Start(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrStartWorkflow, err))
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}
	ret := &runtimev1pb.WorkflowReference{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

func (a *UniversalAPI) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	workflowRun := a.WorkflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist, in.WorkflowComponent))
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}

	err := workflowRun.Terminate(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrTerminateWorkflow, err))
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, nil
	}
	return &runtimev1pb.TerminateWorkflowResponse{}, nil
}

func (a *UniversalAPI) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.RaiseEventWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingOrEmptyInstance)
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	if in.EventName == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrMissingWorkflowEventName)
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := status.Errorf(codes.InvalidArgument, messages.ErrNoOrMissingWorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	workflowRun := a.WorkflowComponents[in.WorkflowComponent]
	if workflowRun == nil {
		err := status.Errorf(codes.InvalidArgument, fmt.Sprintf(messages.ErrWorkflowComponentDoesNotExist, in.WorkflowComponent))
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	var inputMap map[string]interface{}
	json.Unmarshal(in.Input, &inputMap)
	req := workflows.RaiseEventRequest{
		InstanceID: in.InstanceId,
		EventName:  in.EventName,
		Input:      inputMap,
	}

	err := workflowRun.RaiseEvent(ctx, &req)
	if err != nil {
		err = status.Errorf(codes.Internal, fmt.Sprintf(messages.ErrRaiseEventWorkflow, err))
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, nil
	}
	return &runtimev1pb.RaiseEventWorkflowResponse{}, nil
}
