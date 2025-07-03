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
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/kit/ptr"
)

// GetWorkflow is the API handler for getting workflow details
func (a *Universal) GetWorkflow(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	req := workflows.GetRequest{
		InstanceID: in.GetInstanceId(),
	}
	response, err := a.workflowEngine.Client().Get(ctx, &req)
	if err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = nil
		} else {
			err = messages.ErrWorkflowGetResponse.WithFormat(in.GetInstanceId(), err)
		}
		a.logger.Debug(err)
		return &runtimev1pb.GetWorkflowResponse{
			InstanceId: in.GetInstanceId(),
		}, err
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

// StartWorkflow is the API handler for starting a workflow
func (a *Universal) StartWorkflow(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
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

	req := workflows.StartRequest{
		Options:       in.GetOptions(),
		WorkflowName:  in.GetWorkflowName(),
		WorkflowInput: wrapperspb.String(string(in.GetInput())),
	}
	if iid := in.GetInstanceId(); iid != "" {
		req.InstanceID = &iid
	}

	policyRunner := resiliency.NewRunner[*workflows.StartResponse](ctx,
		a.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries),
	)
	resp, err := policyRunner(func(ctx context.Context) (*workflows.StartResponse, error) {
		return a.workflowEngine.Client().Start(ctx, &req)
	})
	if err != nil {
		err := messages.ErrStartWorkflow.WithFormat(in.GetWorkflowName(), err)
		a.logger.Debug(err)
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	return &runtimev1pb.StartWorkflowResponse{
		InstanceId: resp.InstanceID,
	}, nil
}

// TerminateWorkflow is the API handler for terminating a workflow
func (a *Universal) TerminateWorkflow(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.TerminateRequest{
		InstanceID: in.GetInstanceId(),
		Recursive:  ptr.Of(true),
	}
	if err := a.workflowEngine.Client().Terminate(ctx, req); err != nil {
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

// RaiseEventWorkflow is the API handler for raising an event to a workflow
func (a *Universal) RaiseEventWorkflow(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
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

	req := workflows.RaiseEventRequest{
		InstanceID: in.GetInstanceId(),
		EventName:  in.GetEventName(),
	}

	if in.GetEventData() != nil {
		req.EventData = wrapperspb.String(string(in.GetEventData()))
	}

	if err := a.workflowEngine.Client().RaiseEvent(ctx, &req); err != nil {
		err = messages.ErrRaiseEventWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}
	return emptyResponse, nil
}

// PauseWorkflow is the API handler for pausing a workflow
func (a *Universal) PauseWorkflow(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.PauseRequest{
		InstanceID: in.GetInstanceId(),
	}
	if err := a.workflowEngine.Client().Pause(ctx, req); err != nil {
		err = messages.ErrPauseWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}

	return emptyResponse, nil
}

// ResumeWorkflow is the API handler for resuming a workflow
func (a *Universal) ResumeWorkflow(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := &workflows.ResumeRequest{
		InstanceID: in.GetInstanceId(),
	}
	if err := a.workflowEngine.Client().Resume(ctx, req); err != nil {
		err = messages.ErrResumeWorkflow.WithFormat(in.GetInstanceId(), err)
		a.logger.Debug(err)
		return emptyResponse, err
	}

	return emptyResponse, nil
}

// PurgeWorkflow is the API handler for purging a workflow
func (a *Universal) PurgeWorkflow(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug(err)
		return emptyResponse, err
	}

	req := workflows.PurgeRequest{
		InstanceID: in.GetInstanceId(),
		Recursive:  ptr.Of(true),
	}

	if err := a.workflowEngine.Client().Purge(ctx, &req); err != nil {
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

// GetWorkflowBeta1 is the API handler for getting workflow details
func (a *Universal) GetWorkflowBeta1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	return a.GetWorkflow(ctx, in)
}

// StartWorkflowBeta1 is the API handler for starting a workflow
func (a *Universal) StartWorkflowBeta1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	return a.StartWorkflow(ctx, in)
}

// TerminateWorkflowBeta1 is the API handler for terminating a workflow
func (a *Universal) TerminateWorkflowBeta1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	return a.TerminateWorkflow(ctx, in)
}

// RaiseEventWorkflowBeta1 is the API handler for raising an event to a workflow
func (a *Universal) RaiseEventWorkflowBeta1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	return a.RaiseEventWorkflow(ctx, in)
}

// PauseWorkflowBeta1 is the API handler for pausing a workflow
func (a *Universal) PauseWorkflowBeta1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	return a.PauseWorkflow(ctx, in)
}

// ResumeWorkflowBeta1 is the API handler for resuming a workflow
func (a *Universal) ResumeWorkflowBeta1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	return a.ResumeWorkflow(ctx, in)
}

// PurgeWorkflowBeta1 is the API handler for purging a workflow
func (a *Universal) PurgeWorkflowBeta1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	return a.PurgeWorkflow(ctx, in)
}

// GetWorkflowAlpha1 is the API handler for getting workflow details
// Deprecated: Use GetWorkflow instead.
func (a *Universal) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	return a.GetWorkflow(ctx, in)
}

// StartWorkflowAlpha1 is the API handler for starting a workflow
// Deprecated: Use StartWorkflow instead.
func (a *Universal) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	return a.StartWorkflow(ctx, in)
}

// TerminateWorkflowAlpha1 is the API handler for terminating a workflow
// Deprecated: Use TerminateWorkflow instead.
func (a *Universal) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	return a.TerminateWorkflow(ctx, in)
}

// RaiseEventWorkflowAlpha1 is the API handler for raising an event to a workflow
// Deprecated: Use RaiseEventWorkflow instead.
func (a *Universal) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	return a.RaiseEventWorkflow(ctx, in)
}

// PauseWorkflowAlpha1 is the API handler for pausing a workflow
// Deprecated: Use PauseWorkflow instead.
func (a *Universal) PauseWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	return a.PauseWorkflow(ctx, in)
}

// ResumeWorkflowAlpha1 is the API handler for resuming a workflow
// Deprecated: Use ResumeWorkflow instead.
func (a *Universal) ResumeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	return a.ResumeWorkflow(ctx, in)
}

// PurgeWorkflowAlpha1 is the API handler for purging a workflow
// Deprecated: Use PurgeWorkflow instead.
func (a *Universal) PurgeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	return a.PurgeWorkflow(ctx, in)
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
