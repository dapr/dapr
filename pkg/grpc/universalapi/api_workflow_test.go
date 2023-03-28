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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
)

var (
	PauseWorkflow  = "PauseWorkflow"
	ResumeWorkflow = "ResumeWorkflow"
)

func TestPauseResumeWorkflow(t *testing.T) {
	fakeWorkflowComponent := daprt.MockWorkflow{}
	fakeWorkflows := map[string]workflows.Workflow{
		"fakeWorkflow": &fakeWorkflowComponent,
	}

	testCases := []struct {
		testName          string
		apiToBetested     string
		instanceID        string
		workflowComponent string
		errorExcepted     bool
		expectedError     messages.APIError
	}{
		{
			testName:          "No instance id present in pause request",
			apiToBetested:     PauseWorkflow,
			instanceID:        "",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     true,
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "No workflow component provided in pause request",
			apiToBetested:     PauseWorkflow,
			instanceID:        "inst1",
			workflowComponent: "",
			errorExcepted:     true,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in pause request",
			apiToBetested:     PauseWorkflow,
			instanceID:        "inst1",
			workflowComponent: "fakeWorkflowNotExist",
			errorExcepted:     true,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "pause for this instance throws error",
			apiToBetested:     PauseWorkflow,
			instanceID:        "errorInstanceId",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     true,
			expectedError:     messages.ErrPauseWorkflow.WithFormat("errorInstanceId"),
		},
		{
			testName:          "All is well in pause request",
			apiToBetested:     PauseWorkflow,
			instanceID:        "instanceId1",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     false,
		},
		{
			testName:          "No instance id present in resume request",
			apiToBetested:     ResumeWorkflow,
			instanceID:        "",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     true,
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "No workflow component provided in resume request",
			apiToBetested:     ResumeWorkflow,
			instanceID:        "inst1",
			workflowComponent: "",
			errorExcepted:     true,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in resume request",
			apiToBetested:     ResumeWorkflow,
			instanceID:        "inst1",
			workflowComponent: "fakeWorkflowNotExist",
			errorExcepted:     true,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "resume for this instance throws error",
			apiToBetested:     ResumeWorkflow,
			instanceID:        "errorInstanceId",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     true,
			expectedError:     messages.ErrResumeWorkflow.WithFormat("errorInstanceId"),
		},
		{
			testName:          "All is well in resume request",
			apiToBetested:     ResumeWorkflow,
			instanceID:        "instanceId1",
			workflowComponent: "fakeWorkflow",
			errorExcepted:     false,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             testLogger,
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.WorkflowActivityRequest{
				InstanceId:        tt.instanceID,
				WorkflowComponent: tt.workflowComponent,
			}
			var err error
			if tt.apiToBetested == PauseWorkflow {
				_, err = fakeAPI.PauseWorkflowAlpha1(context.Background(), req)
			} else if tt.apiToBetested == ResumeWorkflow {
				_, err = fakeAPI.ResumeWorkflowAlpha1(context.Background(), req)
			}

			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}
