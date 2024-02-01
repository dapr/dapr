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
	"time"

	workflowContrib "github.com/dapr/components-contrib/workflows"
)

type MockWorkflow struct{}

const ErrorInstanceID = "errorInstanceID"

var (
	ErrFakeWorkflowComponentError             = errors.New("fake workflow error")
	ErrFakeWorkflowNonRecursiveTerminateError = errors.New("fake workflow non recursive terminate error")
	ErrFakeWorkflowNonRecurisvePurgeError     = errors.New("fake workflow non recursive purge error")
)

func (w *MockWorkflow) Init(metadata workflowContrib.Metadata) error {
	return nil
}

func (w *MockWorkflow) Start(ctx context.Context, req *workflowContrib.StartRequest) (*workflowContrib.StartResponse, error) {
	if req.InstanceID == ErrorInstanceID {
		return nil, ErrFakeWorkflowComponentError
	}
	res := &workflowContrib.StartResponse{
		InstanceID: req.InstanceID,
	}
	return res, nil
}

func (w *MockWorkflow) Terminate(ctx context.Context, req *workflowContrib.TerminateRequest) error {
	if req.InstanceID == ErrorInstanceID {
		return ErrFakeWorkflowComponentError
	}
	if !req.Recursive {
		// Returning fake error to test non recursive terminate
		return ErrFakeWorkflowNonRecursiveTerminateError
	}
	return nil
}

func (w *MockWorkflow) Get(ctx context.Context, req *workflowContrib.GetRequest) (*workflowContrib.StateResponse, error) {
	if req.InstanceID == ErrorInstanceID {
		return nil, ErrFakeWorkflowComponentError
	}
	res := &workflowContrib.StateResponse{
		Workflow: &workflowContrib.WorkflowState{
			InstanceID:    req.InstanceID,
			WorkflowName:  "mockWorkflowName",
			CreatedAt:     time.Now(),
			LastUpdatedAt: time.Now(),
			RuntimeStatus: "TESTING",
		},
	}
	return res, nil
}

func (w *MockWorkflow) RaiseEvent(ctx context.Context, req *workflowContrib.RaiseEventRequest) error {
	if req.InstanceID == ErrorInstanceID {
		return ErrFakeWorkflowComponentError
	}
	return nil
}

func (w *MockWorkflow) Pause(ctx context.Context, req *workflowContrib.PauseRequest) error {
	if req.InstanceID == ErrorInstanceID {
		return ErrFakeWorkflowComponentError
	}
	return nil
}

func (w *MockWorkflow) Resume(ctx context.Context, req *workflowContrib.ResumeRequest) error {
	if req.InstanceID == ErrorInstanceID {
		return ErrFakeWorkflowComponentError
	}
	return nil
}

func (w *MockWorkflow) Purge(ctx context.Context, req *workflowContrib.PurgeRequest) error {
	if req.InstanceID == ErrorInstanceID {
		return ErrFakeWorkflowComponentError
	}
	if !req.Recursive {
		// Returning fake error to test non recursive purge
		return ErrFakeWorkflowNonRecurisvePurgeError
	}
	return nil
}
