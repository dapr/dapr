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
	"time"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func (a *UniversalAPI) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}
	response, err := workflowComponent.Get(ctx, &req)
	if err != nil {
		innerErr := messages.ErrWorkflowGetResponse.WithFormat(in.InstanceId, err)
		a.Logger.Debug(innerErr)
		return &runtimev1pb.GetWorkflowResponse{}, innerErr
	}

	id := &runtimev1pb.WorkflowReference{
		InstanceId: response.WFInfo.InstanceID,
	}

	t, err := time.Parse(time.RFC3339, response.StartTime)
	if err != nil {
		innerErr := messages.ErrTimerParse.WithFormat(err)
		a.Logger.Debug(innerErr)
		return &runtimev1pb.GetWorkflowResponse{}, innerErr
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
		err := messages.ErrWorkflowNameMissing
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
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

	resp, err := workflowComponent.Start(ctx, &req)
	if err != nil {
		innerErr := messages.ErrStartWorkflow.WithFormat(in.WorkflowName, err)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, innerErr
	}
	ret := &runtimev1pb.WorkflowReference{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

func (a *UniversalAPI) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*runtimev1pb.TerminateWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}

	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}

	err := workflowComponent.Terminate(ctx, &req)
	if err != nil {
		err = messages.ErrTerminateWorkflow.WithFormat(in.InstanceId)
		a.Logger.Debug(err)
		return &runtimev1pb.TerminateWorkflowResponse{}, err
	}
	return &runtimev1pb.TerminateWorkflowResponse{}, nil
}

func (a *UniversalAPI) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.RaiseEventWorkflowResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	if in.EventName == "" {
		err := messages.ErrMissingWorkflowEventName
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}

	req := workflows.RaiseEventRequest{
		InstanceID: in.InstanceId,
		EventName:  in.EventName,
		Input:      in.Input,
	}

	err := workflowComponent.RaiseEvent(ctx, &req)
	if err != nil {
		err = messages.ErrRaiseEventWorkflow.WithFormat(in.InstanceId)
		a.Logger.Debug(err)
		return &runtimev1pb.RaiseEventWorkflowResponse{}, err
	}
	return &runtimev1pb.RaiseEventWorkflowResponse{}, nil
}
