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
		err := messages.ErrWorkflowGetResponse.WithFormat(in.InstanceId, err)
		a.Logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	id := &runtimev1pb.WorkflowReference{
		InstanceId: response.WFInfo.InstanceID,
	}

	t, err := time.Parse(time.RFC3339, response.StartTime)
	if err != nil {
		err := messages.ErrTimerParse.WithFormat(err)
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

	var inputMap map[string]interface{}
	json.Unmarshal(in.Input, &inputMap)
	req := workflows.StartRequest{
		InstanceID:   in.InstanceId,
		Options:      in.Options,
		WorkflowName: in.WorkflowName,
		Input:        inputMap,
	}

	resp, err := workflowComponent.Start(ctx, &req)
	if err != nil {
		err := messages.ErrStartWorkflow.WithFormat(in.WorkflowName, err)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowReference{}, err
	}
	ret := &runtimev1pb.WorkflowReference{
		InstanceId: resp.InstanceID,
	}
	return ret, nil
}

// TerminateWorkflowAlpha1 is the API handler for terminating a workflow
func (a *UniversalAPI) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.WorkflowActivityRequest) (*runtimev1pb.WorkflowActivityResponse, error) {
	var method func(context.Context, *workflows.WorkflowReference) error
	if in.WorkflowComponent != "" && a.WorkflowComponents[in.WorkflowComponent] != nil {
		method = a.WorkflowComponents[in.WorkflowComponent].Terminate
	}
	return a.workflowActivity(ctx, in, method, messages.ErrTerminateWorkflow)
}

func (a *UniversalAPI) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*runtimev1pb.WorkflowActivityResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	if in.EventName == "" {
		err := messages.ErrMissingWorkflowEventName
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
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
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}
	return &runtimev1pb.WorkflowActivityResponse{}, nil
}

// PauseWorkflowAlpha1 is the API handler for pausing a workflow
func (a *UniversalAPI) PauseWorkflowAlpha1(ctx context.Context, in *runtimev1pb.WorkflowActivityRequest) (*runtimev1pb.WorkflowActivityResponse, error) {
	var method func(context.Context, *workflows.WorkflowReference) error
	if in.WorkflowComponent != "" && a.WorkflowComponents[in.WorkflowComponent] != nil {
		method = a.WorkflowComponents[in.WorkflowComponent].Pause
	}
	return a.workflowActivity(ctx, in, method, messages.ErrPauseWorkflow)
}

// ResumeWorkflowAlpha1 is the API handler for resuming a workflow
func (a *UniversalAPI) ResumeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.WorkflowActivityRequest) (*runtimev1pb.WorkflowActivityResponse, error) {
	var method func(context.Context, *workflows.WorkflowReference) error
	if in.WorkflowComponent != "" && a.WorkflowComponents[in.WorkflowComponent] != nil {
		method = a.WorkflowComponents[in.WorkflowComponent].Resume
	}
	return a.workflowActivity(ctx, in, method, messages.ErrResumeWorkflow)
}

// workflowActivity is a helper function to handle workflow requests for pause, resume, and terminate
func (a *UniversalAPI) workflowActivity(ctx context.Context, in *runtimev1pb.WorkflowActivityRequest, method func(context.Context, *workflows.WorkflowReference) error, methodErr messages.APIError) (*runtimev1pb.WorkflowActivityResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	req := workflows.WorkflowReference{
		InstanceID: in.InstanceId,
	}

	err := method(ctx, &req)
	if err != nil {
		err = methodErr.WithFormat(in.InstanceId)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}
	return &runtimev1pb.WorkflowActivityResponse{}, nil
}

func (a *UniversalAPI) PurgeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*runtimev1pb.WorkflowActivityResponse, error) {
	if in.InstanceId == "" {
		err := messages.ErrMissingOrEmptyInstance
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	if in.WorkflowComponent == "" {
		err := messages.ErrNoOrMissingWorkflowComponent
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	workflowComponent := a.WorkflowComponents[in.WorkflowComponent]
	if workflowComponent == nil {
		err := messages.ErrWorkflowComponentDoesNotExist.WithFormat(in.WorkflowComponent)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}

	req := workflows.PurgeRequest{
		InstanceID: in.InstanceId,
	}

	err := workflowComponent.Purge(ctx, &req)
	if err != nil {
		err = messages.ErrPurgeWorkflow.WithFormat(in.InstanceId)
		a.Logger.Debug(err)
		return &runtimev1pb.WorkflowActivityResponse{}, err
	}
	return &runtimev1pb.WorkflowActivityResponse{}, nil
}
