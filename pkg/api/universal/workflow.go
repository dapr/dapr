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
	"fmt"
	"time"
	"unicode"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/kit/logger"
)

// Status values are defined at: https://github.com/dapr/durabletask-go/blob/119b361079c45e368f83b223888d56a436ac59b9/internal/protos/orchestrator_service.pb.go#L42-L64
var statusMap = map[int32]string{
	0: "RUNNING",
	1: "COMPLETED",
	2: "CONTINUED_AS_NEW",
	3: "FAILED",
	4: "CANCELED",
	5: "TERMINATED",
	6: "PENDING",
	7: "SUSPENDED",
}

func getStatusString(status int32) string {
	if statusStr, ok := statusMap[status]; ok {
		return statusStr
	}

	return "UNKNOWN"
}

// GetWorkflow is the API handler for getting workflow details
func (a *Universal) GetWorkflow(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.GetWorkflowResponse{}, err
	}
	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.GetWorkflowResponse{}, err
	}

	var opts []api.FetchWorkflowMetadataOptions
	if targetAppID != "" {
		opts = append(opts, api.WithFetchAppID(targetAppID))
	}
	metadata, err := a.workflowEngine.Client().FetchWorkflowMetadata(ctx, api.InstanceID(in.GetInstanceId()), opts...)
	if err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = nil
		} else {
			err = messages.ErrWorkflowGetResponse.WithFormat(in.GetInstanceId(),
				fmt.Errorf("failed to get workflow metadata for '%s': %w", in.GetInstanceId(), err))
		}
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.GetWorkflowResponse{
			InstanceId: in.GetInstanceId(),
		}, err
	}

	res := &runtimev1pb.GetWorkflowResponse{
		InstanceId:    in.GetInstanceId(),
		WorkflowName:  metadata.GetName(),
		CreatedAt:     timestamppb.New(metadata.GetCreatedAt().AsTime()),
		LastUpdatedAt: timestamppb.New(metadata.GetLastUpdatedAt().AsTime()),
		RuntimeStatus: getStatusString(int32(metadata.GetRuntimeStatus())),
		Properties:    make(map[string]string),
	}

	if metadata.GetCustomStatus() != nil {
		res.Properties["dapr.workflow.custom_status"] = metadata.GetCustomStatus().GetValue()
	}

	if metadata.Input != nil {
		res.Properties["dapr.workflow.input"] = metadata.GetInput().GetValue()
	}

	// A failed workflow has no successful output: durabletask-go stores the
	// failure message in the completed-event result (surfaced here as Output),
	// but the failure is already reported via dapr.workflow.failure.* below, so
	// it must not also be exposed as dapr.workflow.output.
	if metadata.Output != nil && metadata.FailureDetails == nil {
		res.Properties["dapr.workflow.output"] = metadata.GetOutput().GetValue()
	}

	// Status-specific fields
	if metadata.FailureDetails != nil {
		res.Properties["dapr.workflow.failure.error_type"] = metadata.GetFailureDetails().GetErrorType()
		res.Properties["dapr.workflow.failure.error_message"] = metadata.GetFailureDetails().GetErrorMessage()
		if trace := metadata.GetFailureDetails().GetStackTrace(); trace != nil {
			res.Properties["dapr.workflow.failure.stack_trace"] = trace.GetValue()
		}
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
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	if in.GetWorkflowName() == "" {
		err := messages.ErrWorkflowNameMissing
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.StartWorkflowResponse{}, err
	}
	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	opts := make([]api.NewWorkflowOptions, 0, 4)
	opts = append(opts,
		api.WithInstanceID(api.InstanceID(in.GetInstanceId())),
		// Inputs are expected to be unprocessed string values (e.g. JSON text).
		api.WithRawInput(wrapperspb.String(string(in.GetInput()))),
	)
	if targetAppID != "" {
		opts = append(opts, api.WithAppID(targetAppID))
	}

	// Start time is optional and must be in the RFC3339 format (e.g. 2009-11-10T23:00:00Z).
	if startTimeRFC3339, ok := in.GetOptions()["dapr.workflow.start_time"]; ok {
		startTime, terr := time.Parse(time.RFC3339, startTimeRFC3339)
		if terr != nil {
			err := messages.ErrStartWorkflow.WithFormat(in.GetWorkflowName(),
				errors.New(`start times must be in RFC3339 format (e.g. "2009-11-10T23:00:00Z")`))
			a.logger.Debug("api call returned error", logger.Err(err))
			return &runtimev1pb.StartWorkflowResponse{}, err
		}
		opts = append(opts, api.WithStartTime(startTime))
	}

	policyRunner := resiliency.NewRunner[api.InstanceID](ctx,
		a.resiliency.BuiltInPolicy(resiliency.BuiltInActorRetries),
	)
	workflowID, err := policyRunner(func(ctx context.Context) (api.InstanceID, error) {
		id, serr := a.workflowEngine.Client().ScheduleNewWorkflow(ctx, in.GetWorkflowName(), opts...)
		if serr != nil {
			return id, fmt.Errorf("unable to start workflow: %w", serr)
		}
		return id, nil
	})
	if err != nil {
		err := messages.ErrStartWorkflow.WithFormat(in.GetWorkflowName(), err)
		a.logger.Debug("api call returned error", logger.Err(err))
		return &runtimev1pb.StartWorkflowResponse{}, err
	}

	return &runtimev1pb.StartWorkflowResponse{
		InstanceId: string(workflowID),
	}, nil
}

// TerminateWorkflow is the API handler for terminating a workflow
func (a *Universal) TerminateWorkflow(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	if _, err := a.ActorRouter(ctx); err != nil {
		return nil, err
	}
	emptyResponse := &emptypb.Empty{}
	if err := a.validateInstanceID(in.GetInstanceId(), false /* isCreate */); err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	opts := []api.TerminateOptions{api.WithRecursiveTerminate(true)}
	if targetAppID != "" {
		opts = append(opts, api.WithTerminateAppID(targetAppID))
	}
	if err := a.workflowEngine.Client().TerminateWorkflow(ctx, api.InstanceID(in.GetInstanceId()), opts...); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = messages.ErrWorkflowInstanceNotFound.WithFormat(in.GetInstanceId())
		} else {
			err = messages.ErrTerminateWorkflow.WithFormat(in.GetInstanceId(),
				fmt.Errorf("failed to terminate workflow %s: %w", in.GetInstanceId(), err))
		}
		a.logger.Debug("api call returned error", logger.Err(err))
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
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	if in.GetEventName() == "" {
		err := messages.ErrMissingWorkflowEventName
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	// Event data is optional; when set, it is expected to be an unprocessed
	// string value (e.g. JSON text).
	var opts []api.RaiseEventOptions
	if targetAppID != "" {
		opts = append(opts, api.WithRaiseEventAppID(targetAppID))
	}
	if in.GetEventData() != nil {
		opts = append(opts, api.WithRawEventData(wrapperspb.String(string(in.GetEventData()))))
	}

	if err := a.workflowEngine.Client().RaiseEvent(ctx, api.InstanceID(in.GetInstanceId()), in.GetEventName(), opts...); err != nil {
		err = messages.ErrRaiseEventWorkflow.WithFormat(in.GetInstanceId(),
			fmt.Errorf("failed to raise event %s on workflow %s: %w", in.GetEventName(), in.GetInstanceId(), err))
		a.logger.Debug("api call returned error", logger.Err(err))
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
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	var opts []api.SuspendOptions
	if targetAppID != "" {
		opts = append(opts, api.WithSuspendAppID(targetAppID))
	}
	if err := a.workflowEngine.Client().SuspendWorkflow(ctx, api.InstanceID(in.GetInstanceId()), "", opts...); err != nil {
		err = messages.ErrPauseWorkflow.WithFormat(in.GetInstanceId(),
			fmt.Errorf("failed to pause workflow %s: %w", in.GetInstanceId(), err))
		a.logger.Debug("api call returned error", logger.Err(err))
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
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	var opts []api.ResumeOptions
	if targetAppID != "" {
		opts = append(opts, api.WithResumeAppID(targetAppID))
	}
	if err := a.workflowEngine.Client().ResumeWorkflow(ctx, api.InstanceID(in.GetInstanceId()), "", opts...); err != nil {
		err = messages.ErrResumeWorkflow.WithFormat(in.GetInstanceId(),
			fmt.Errorf("failed to resume workflow %s: %w", in.GetInstanceId(), err))
		a.logger.Debug("api call returned error", logger.Err(err))
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
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	targetAppID, err := a.targetAppID(in.GetAppId())
	if err != nil {
		a.logger.Debug("api call returned error", logger.Err(err))
		return emptyResponse, err
	}

	opts := []api.PurgeOptions{api.WithRecursivePurge(true)}
	if targetAppID != "" {
		opts = append(opts, api.WithPurgeAppID(targetAppID))
	}
	if err := a.workflowEngine.Client().PurgeWorkflowState(ctx, api.InstanceID(in.GetInstanceId()), opts...); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			err = messages.ErrWorkflowInstanceNotFound.WithFormat(in.GetInstanceId())
		} else {
			err = messages.ErrPurgeWorkflow.WithFormat(in.GetInstanceId(),
				fmt.Errorf("failed to Purge workflow %s: %w", in.GetInstanceId(), err))
		}
		a.logger.Debug("api call returned error", logger.Err(err))
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
//
// Deprecated: Use GetWorkflow instead.
func (a *Universal) GetWorkflowAlpha1(ctx context.Context, in *runtimev1pb.GetWorkflowRequest) (*runtimev1pb.GetWorkflowResponse, error) {
	return a.GetWorkflow(ctx, in)
}

// StartWorkflowAlpha1 is the API handler for starting a workflow
//
// Deprecated: Use StartWorkflow instead.
func (a *Universal) StartWorkflowAlpha1(ctx context.Context, in *runtimev1pb.StartWorkflowRequest) (*runtimev1pb.StartWorkflowResponse, error) {
	return a.StartWorkflow(ctx, in)
}

// TerminateWorkflowAlpha1 is the API handler for terminating a workflow
//
// Deprecated: Use TerminateWorkflow instead.
func (a *Universal) TerminateWorkflowAlpha1(ctx context.Context, in *runtimev1pb.TerminateWorkflowRequest) (*emptypb.Empty, error) {
	return a.TerminateWorkflow(ctx, in)
}

// RaiseEventWorkflowAlpha1 is the API handler for raising an event to a workflow
//
// Deprecated: Use RaiseEventWorkflow instead.
func (a *Universal) RaiseEventWorkflowAlpha1(ctx context.Context, in *runtimev1pb.RaiseEventWorkflowRequest) (*emptypb.Empty, error) {
	return a.RaiseEventWorkflow(ctx, in)
}

// PauseWorkflowAlpha1 is the API handler for pausing a workflow
//
// Deprecated: Use PauseWorkflow instead.
func (a *Universal) PauseWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PauseWorkflowRequest) (*emptypb.Empty, error) {
	return a.PauseWorkflow(ctx, in)
}

// ResumeWorkflowAlpha1 is the API handler for resuming a workflow
//
// Deprecated: Use ResumeWorkflow instead.
func (a *Universal) ResumeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.ResumeWorkflowRequest) (*emptypb.Empty, error) {
	return a.ResumeWorkflow(ctx, in)
}

// PurgeWorkflowAlpha1 is the API handler for purging a workflow
//
// Deprecated: Use PurgeWorkflow instead.
func (a *Universal) PurgeWorkflowAlpha1(ctx context.Context, in *runtimev1pb.PurgeWorkflowRequest) (*emptypb.Empty, error) {
	return a.PurgeWorkflow(ctx, in)
}

// targetAppID validates the optional app ID of a workflow request and returns
// the value to set on the component request. An empty app ID or the local app
// ID means a local operation and returns empty. The character set is
// restricted like instance IDs, in particular rejecting '.' so a caller
// cannot smuggle extra segments into the derived actor type name
// "dapr.internal.<namespace>.<appID>.workflow".
func (a *Universal) targetAppID(appID string) (string, error) {
	if appID == "" || appID == a.AppID() {
		return "", nil
	}
	if !common.ValidAppID(appID) {
		return "", messages.ErrInvalidWorkflowAppID.WithFormat(appID)
	}
	return appID, nil
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
