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
package wfengine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/workflows"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1" // This will be removed
	"github.com/dapr/kit/logger"
)

var ComponentDefinition = componentsV1alpha1.Component{
	TypeMeta: metav1.TypeMeta{
		Kind: "Component",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name: "dapr",
	},
	Spec: componentsV1alpha1.ComponentSpec{
		Type:     "workflow.dapr",
		Version:  "v1",
		Metadata: []commonapi.NameValuePair{},
	},
}

// Status values are defined at: https://github.com/microsoft/durabletask-go/blob/119b361079c45e368f83b223888d56a436ac59b9/internal/protos/orchestrator_service.pb.go#L42-L64
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

func BuiltinWorkflowFactory(engine *WorkflowEngine) func(logger.Logger) workflows.Workflow {
	return func(logger logger.Logger) workflows.Workflow {
		return &workflowEngineComponent{
			logger: logger,
			client: backend.NewTaskHubClient(engine.backend),
		}
	}
}

type workflowEngineComponent struct {
	logger logger.Logger
	client backend.TaskHubClient
}

func (c *workflowEngineComponent) Init(metadata workflows.Metadata) error {
	c.logger.Info("initializing Dapr workflow component")
	return nil
}

func (c *workflowEngineComponent) Start(ctx context.Context, req *workflows.StartRequest) (*workflows.StartResponse, error) {
	if req.WorkflowName == "" {
		return nil, errors.New("a workflow name is required")
	}

	// Specifying the ID is optional - if not specified, a random ID will be generated by the client.
	var opts []api.NewOrchestrationOptions
	if req.InstanceID != "" {
		opts = append(opts, api.WithInstanceID(api.InstanceID(req.InstanceID)))
	}

	// Input is also optional. However, inputs are expected to be unprocessed string values (e.g. JSON text)
	if len(req.WorkflowInput) > 0 {
		opts = append(opts, api.WithRawInput(string(req.WorkflowInput)))
	}

	// Start time is also optional and must be in the RFC3339 format (e.g. 2009-11-10T23:00:00Z).
	if req.Options != nil {
		if startTimeRFC3339, ok := req.Options["dapr.workflow.start_time"]; ok {
			if startTime, err := time.Parse(time.RFC3339, startTimeRFC3339); err != nil {
				return nil, fmt.Errorf("start times must be in RFC3339 format (e.g. \"2009-11-10T23:00:00Z\")")
			} else {
				opts = append(opts, api.WithStartTime(startTime))
			}
		}
	}

	var workflowID api.InstanceID
	var err error

	workflowID, err = c.client.ScheduleNewOrchestration(ctx, req.WorkflowName, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to start workflow: %w", err)
	}

	c.logger.Infof("created new workflow instance with ID '%s'", workflowID)
	res := &workflows.StartResponse{
		InstanceID: string(workflowID),
	}
	return res, nil
}

func (c *workflowEngineComponent) Terminate(ctx context.Context, req *workflows.TerminateRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.TerminateOrchestration(ctx, api.InstanceID(req.InstanceID), ""); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Infof("No such instance exists: '%s'", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to terminate workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Infof("scheduled termination for workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *workflowEngineComponent) Purge(ctx context.Context, req *workflows.PurgeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.PurgeOrchestrationState(ctx, api.InstanceID(req.InstanceID)); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Infof("Unable to purge the instance: '%s', no such instance exists", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to Purge workflow %s: %w", req.InstanceID, err)
	}

	return nil
}

func (c *workflowEngineComponent) RaiseEvent(ctx context.Context, req *workflows.RaiseEventRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if req.EventName == "" {
		return errors.New("an event name is required")
	}

	// Input is also optional. However, inputs are expected to be unprocessed string values (e.g. JSON text)
	var opts []api.RaiseEventOptions
	if len(req.EventData) > 0 {
		opts = append(opts, api.WithRawEventData(string(req.EventData)))
	}

	if err := c.client.RaiseEvent(ctx, api.InstanceID(req.InstanceID), req.EventName, opts...); err != nil {
		return fmt.Errorf("failed to raise event %s on workflow %s: %w", req.EventName, req.InstanceID, err)
	}

	c.logger.Infof("Raised event %s on workflow instance '%s'", req.EventName, req.InstanceID)
	return nil
}

func (c *workflowEngineComponent) Get(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
	if req.InstanceID == "" {
		return nil, errors.New("a workflow instance ID is required")
	}

	if metadata, err := c.client.FetchOrchestrationMetadata(ctx, api.InstanceID(req.InstanceID)); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Infof("Unable to get data on the instance: %s, no such instance exists", req.InstanceID)
			return nil, err
		}
		return nil, fmt.Errorf("failed to get workflow metadata for '%s': %w", req.InstanceID, err)
	} else {
		res := &workflows.StateResponse{
			Workflow: &workflows.WorkflowState{
				InstanceID:    req.InstanceID,
				WorkflowName:  metadata.Name,
				CreatedAt:     metadata.CreatedAt,
				LastUpdatedAt: metadata.LastUpdatedAt,
				RuntimeStatus: getStatusString(int32(metadata.RuntimeStatus)),
				Properties: map[string]string{
					"dapr.workflow.input":         metadata.SerializedInput,
					"dapr.workflow.custom_status": metadata.SerializedCustomStatus,
				},
			},
		}

		// Status-specific fields
		if metadata.FailureDetails != nil {
			res.Workflow.Properties["dapr.workflow.failure.error_type"] = metadata.FailureDetails.ErrorType
			res.Workflow.Properties["dapr.workflow.failure.error_message"] = metadata.FailureDetails.ErrorMessage
		} else if metadata.IsComplete() {
			res.Workflow.Properties["dapr.workflow.output"] = metadata.SerializedOutput
		}

		return res, nil
	}
}

func (c *workflowEngineComponent) Pause(ctx context.Context, req *workflows.PauseRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.SuspendOrchestration(ctx, api.InstanceID(req.InstanceID), ""); err != nil {
		return fmt.Errorf("failed to pause workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Infof("pausing workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *workflowEngineComponent) Resume(ctx context.Context, req *workflows.ResumeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.ResumeOrchestration(ctx, api.InstanceID(req.InstanceID), ""); err != nil {
		return fmt.Errorf("failed to resume workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Infof("resuming workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *workflowEngineComponent) PurgeWorkflow(ctx context.Context, req *workflows.PurgeRequest) error {
	if req.InstanceID == "" {
		return errors.New("a workflow instance ID is required")
	}

	if err := c.client.PurgeOrchestrationState(ctx, api.InstanceID(req.InstanceID)); err != nil {
		if errors.Is(err, api.ErrInstanceNotFound) {
			c.logger.Infof("The requested instance: '%s' does not exist or has already been purged", req.InstanceID)
			return err
		}
		return fmt.Errorf("failed to purge workflow %s: %w", req.InstanceID, err)
	}

	c.logger.Infof("purging workflow instance '%s'", req.InstanceID)
	return nil
}

func (c *workflowEngineComponent) GetComponentMetadata() map[string]string {
	return nil
}

func getStatusString(status int32) string {
	if statusStr, ok := statusMap[status]; ok {
		return statusStr
	}
	return "UNKNOWN"
}
