/*
Copyright 2024 The Dapr Authors
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
package wfengine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"

	"github.com/dapr/components-contrib/workflows"
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

type client struct {
	logger logger.Logger
	client backend.TaskHubClient
}

func (c *client) Init(metadata workflows.Metadata) error {
	c.logger.Info("Initializing Dapr workflow component")
	return nil
}

func (c *client) Start(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error) {
	if req.WorkflowName == "" {
		return nil, errors.New("a workflow name is required")
	}

	// Init with capacity of 3 as worst-case scenario
	opts := make([]api.NewOrchestrationOptions, 0, 3)

	// Specifying the ID is optional - if not specified, a random ID will be generated by the client.
	if req.InstanceID != nil {
		opts = append(opts, api.WithInstanceID(api.InstanceID(*req.InstanceID)))
	}

	// Input is also optional. However, inputs are expected to be unprocessed string values (e.g. JSON text)
	if req.WorkflowInput != nil {
		opts = append(opts, api.WithRawInput(req.WorkflowInput))
	}

	// Start time is also optional and must be in the RFC3339 format (e.g. 2009-11-10T23:00:00Z).
	if req.Options != nil {
		if startTimeRFC3339, ok := req.Options["dapr.workflow.start_time"]; ok {
			if startTime, err := time.Parse(time.RFC3339, startTimeRFC3339); err != nil {
				return nil, errors.New(`start times must be in RFC3339 format (e.g. "2009-11-10T23:00:00Z")`)
			} else {
				opts = append(opts, api.WithStartTime(startTime))
			}
		}
	}

	workflowID, err := c.client.ScheduleNewOrchestration(ctx, req.WorkflowName, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to start workflow: %w", err)
	}

	c.logger.Debugf("Created new workflow '%s' instance with ID '%s'", req.WorkflowName, workflowID)
	res := &workflows.StartResponse{
		InstanceID: string(workflowID),
	}
	return res, nil
}

func (c *client) Terminate(ctx context.Context, req *workflows.TerminateRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	var opts []api.TerminateOptions
	if req.Recursive != nil {
		opts = append(opts, api.WithRecursiveTerminate(*req.Recursive))
	}
	if err := c.client.TerminateOrchestration(ctx, api.InstanceID(req.InstanceID), opts...); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Infof("No such instance exists: '%s'", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to terminate workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Debugf("Scheduled termination for workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *client) Purge(ctx context.Context, req *workflows.PurgeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	var opts []api.PurgeOptions
	if req.Recursive != nil {
		opts = append(opts, api.WithRecursivePurge(*req.Recursive))
	}
	if err := c.client.PurgeOrchestrationState(ctx, api.InstanceID(req.InstanceID), opts...); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Warnf("Unable to purge the instance: '%s', no such instance exists", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to Purge workflow %s: %w", req.InstanceID, err)
	}
	c.logger.Debugf("Purging workflow instance '%s'", req.InstanceID)

	return nil
}

func (c *client) RaiseEvent(ctx context.Context, req *workflows.RaiseEventRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if req.EventName == "" {
		return errors.New("an event name is required")
	}

	// Input is also optional. However, inputs are expected to be unprocessed string values (e.g. JSON text)
	var opts []api.RaiseEventOptions
	if req.EventData != nil {
		opts = append(opts, api.WithRawEventData(req.EventData))
	}

	if err := c.client.RaiseEvent(ctx, api.InstanceID(req.InstanceID), req.EventName, opts...); err != nil {
		return fmt.Errorf("failed to raise event %s on workflow %s: %w", req.EventName, req.InstanceID, err)
	}

	c.logger.Debugf("Raised event %s on workflow instance '%s'", req.EventName, req.InstanceID)
	return nil
}

func (c *client) Get(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
	if req.InstanceID == "" {
		return nil, errors.New("a workflow instance ID is required")
	}

	metadata, err := c.client.FetchOrchestrationMetadata(ctx, api.InstanceID(req.InstanceID))
	if err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Errorf("Unable to get data on the instance: %s, no such instance exists", req.InstanceID)
			return nil, err
		}
		return nil, fmt.Errorf("failed to get workflow metadata for '%s': %w", req.InstanceID, err)
	}

	res := &workflows.StateResponse{
		Workflow: &workflows.WorkflowState{
			InstanceID:    req.InstanceID,
			WorkflowName:  metadata.GetName(),
			CreatedAt:     metadata.GetCreatedAt().AsTime(),
			LastUpdatedAt: metadata.GetLastUpdatedAt().AsTime(),
			RuntimeStatus: getStatusString(int32(metadata.GetRuntimeStatus())),
			Properties:    make(map[string]string),
		},
	}

	if metadata.GetCustomStatus() != nil {
		res.Workflow.Properties["dapr.workflow.custom_status"] = metadata.GetCustomStatus().GetValue()
	}

	if metadata.Input != nil {
		res.Workflow.Properties["dapr.workflow.input"] = metadata.GetInput().GetValue()
	}

	if metadata.Output != nil {
		res.Workflow.Properties["dapr.workflow.output"] = metadata.GetOutput().GetValue()
	}

	// Status-specific fields
	if metadata.FailureDetails != nil {
		res.Workflow.Properties["dapr.workflow.failure.error_type"] = metadata.GetFailureDetails().GetErrorType()
		res.Workflow.Properties["dapr.workflow.failure.error_message"] = metadata.GetFailureDetails().GetErrorMessage()
	}

	return res, nil
}

func (c *client) Pause(ctx context.Context, req *workflows.PauseRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.SuspendOrchestration(ctx, api.InstanceID(req.InstanceID), ""); err != nil {
		return fmt.Errorf("failed to pause workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Debugf("Pausing workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *client) Resume(ctx context.Context, req *workflows.ResumeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.ResumeOrchestration(ctx, api.InstanceID(req.InstanceID), ""); err != nil {
		return fmt.Errorf("failed to resume workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Debugf("Resuming workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *client) PurgeWorkflow(ctx context.Context, req *workflows.PurgeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.PurgeOrchestrationState(ctx, api.InstanceID(req.InstanceID)); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Warnf("The requested instance: '%s' does not exist or has already been purged", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to purge workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Debugf("Purging workflow instance '%s'", req.InstanceID)
	return nil
}

func getStatusString(status int32) string {
	if statusStr, ok := statusMap[status]; ok {
		return statusStr
	}
	return "UNKNOWN"
}

func (c *client) Close() error {
	return nil
}
