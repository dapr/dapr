package testing

import (
	"context"
	"errors"

	workflowContrib "github.com/dapr/components-contrib/workflows"
	mock "github.com/stretchr/testify/mock"
)

type MockWorkflow struct {
	mock.Mock
}

func (w MockWorkflow) Init(metadata workflowContrib.Metadata) error {
	return nil
}

func (w MockWorkflow) Start(ctx context.Context, req *workflowContrib.StartRequest) (*workflowContrib.WorkflowReference, error) {
	return nil, nil
}

func (w MockWorkflow) Terminate(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	return nil
}

func (w MockWorkflow) Get(ctx context.Context, req *workflowContrib.WorkflowReference) (*workflowContrib.StateResponse, error) {
	return nil, nil
}

func (w MockWorkflow) RaiseEvent(ctx context.Context, req *workflowContrib.RaiseEventRequest) error {
	return nil
}

func (w MockWorkflow) Pause(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	if req.InstanceID == "errorInstanceId" {
		return errors.New("error encountered in pause")
	}
	return nil
}

func (w MockWorkflow) Resume(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	if req.InstanceID == "errorInstanceId" {
		return errors.New("error encountered in resume")
	}
	return nil
}
