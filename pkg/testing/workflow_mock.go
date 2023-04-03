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

package testing

import (
	"context"
	"errors"

	mock "github.com/stretchr/testify/mock"

	workflowContrib "github.com/dapr/components-contrib/workflows"
)

type MockWorkflow struct {
	mock.Mock
}

func (w *MockWorkflow) Init(metadata workflowContrib.Metadata) error {
	return nil
}

func (w *MockWorkflow) Start(ctx context.Context, req *workflowContrib.StartRequest) (*workflowContrib.WorkflowReference, error) {
	return nil, nil
}

func (w *MockWorkflow) Terminate(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	if req.InstanceID == "errorInstanceId" {
		return errors.New("error encountered in terminate")
	}
	return nil

}

func (w *MockWorkflow) Get(ctx context.Context, req *workflowContrib.WorkflowReference) (*workflowContrib.StateResponse, error) {
	return nil, nil
}

func (w *MockWorkflow) RaiseEvent(ctx context.Context, req *workflowContrib.RaiseEventRequest) error {
	return nil
}

func (w *MockWorkflow) Pause(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	if req.InstanceID == "errorInstanceId" {
		return errors.New("error encountered in pause")
	}
	return nil
}

func (w *MockWorkflow) Resume(ctx context.Context, req *workflowContrib.WorkflowReference) error {
	if req.InstanceID == "errorInstanceId" {
		return errors.New("error encountered in resume")
	}
	return nil
}
