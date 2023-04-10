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
	"github.com/dapr/kit/logger"
)

func TestStartWorkflowAPI(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeWorkflowName := "fakeWorkflow"
	fakeInstanceID := "fake-instance-ID__123"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		workflowName      string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in start request",
			workflowComponent: "",
			workflowName:      fakeWorkflowName,
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in start request",
			workflowComponent: "fakeWorkflowNotExist",
			workflowName:      fakeWorkflowName,
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No workflow name provided in start request",
			workflowComponent: fakeComponentName,
			workflowName:      "",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowNameMissing,
		},
		{
			testName:          "No instance ID provided in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "Invalid instance ID provided in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        "invalid#12",
			expectedError:     messages.ErrInvalidInstanceID.WithFormat("invalid#12"),
		},
		{
			testName:          "Too long instance ID provided in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        "this_is_a_very_long_instance_id_that_is_longer_than_64_characters_and_therefore_should_not_be_allowed",
			expectedError:     messages.ErrInstanceIDTooLong.WithFormat(64),
		},
		{
			testName:          "Start for this instance throws error",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        daprt.ErrorInstanceId,
			expectedError:     messages.ErrStartWorkflow.WithFormat(fakeWorkflowName, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.StartWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				WorkflowName:      tt.workflowName,
			}
			_, err := fakeAPI.StartWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}

func TestGetWorkflowAPI(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeInstanceID := "fake_instance_ID_123"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in get request",
			workflowComponent: "",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in get request",
			workflowComponent: "fakeWorkflowNotExist",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No instance ID provided in get request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "Get for this instance throws error",
			workflowComponent: fakeComponentName,
			instanceID:        daprt.ErrorInstanceId,
			expectedError:     messages.ErrWorkflowGetResponse.WithFormat(daprt.ErrorInstanceId, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in get request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.GetWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}

func TestTerminateWorkflowAPI(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeInstanceID := "fake_instance_ID_123"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in terminate request",
			workflowComponent: "",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in terminate request",
			workflowComponent: "fakeWorkflowNotExist",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No instance ID provided in terminate request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "Terminate for this instance throws error",
			workflowComponent: fakeComponentName,
			instanceID:        daprt.ErrorInstanceId,
			expectedError:     messages.ErrTerminateWorkflow.WithFormat(daprt.ErrorInstanceId, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in terminate request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.TerminateWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.TerminateWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}

func TestRaiseEventWorkflowApi(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeInstanceID := "fake_instance_ID_123"
	fakeEventName := "fake_event_name"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		eventName         string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in raise event request",
			workflowComponent: "",
			instanceID:        fakeInstanceID,
			eventName:         fakeEventName,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in raise event request",
			workflowComponent: "fakeWorkflowNotExist",
			instanceID:        fakeInstanceID,
			eventName:         fakeEventName,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No instance ID provided in raise event request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			eventName:         fakeEventName,
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "No event name provided in raise event request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
			eventName:         "",
			expectedError:     messages.ErrMissingWorkflowEventName,
		},
		{
			testName:          "Raise event for this instance throws error",
			workflowComponent: fakeComponentName,
			instanceID:        daprt.ErrorInstanceId,
			eventName:         fakeEventName,
			expectedError:     messages.ErrRaiseEventWorkflow.WithFormat(daprt.ErrorInstanceId, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in raise event request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
			eventName:         fakeEventName,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.RaiseEventWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				EventName:         tt.eventName,
				Input:             []byte("fake_input"),
			}
			_, err := fakeAPI.RaiseEventWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}

func TestPauseWorkflowApi(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeInstanceID := "fake_instance_ID_123"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in pause request",
			workflowComponent: "",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in pause request",
			workflowComponent: "fakeWorkflowNotExist",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No instance ID provided in pause request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "Pause for this instance throws error",
			workflowComponent: fakeComponentName,
			instanceID:        daprt.ErrorInstanceId,
			expectedError:     messages.ErrPauseWorkflow.WithFormat(daprt.ErrorInstanceId, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in pause request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.PauseWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.PauseWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}

func TestResumeWorkflowApi(t *testing.T) {
	fakeComponentName := "fakeWorkflowComponent"
	fakeInstanceID := "fake_instance_ID_123"

	fakeWorkflows := map[string]workflows.Workflow{
		fakeComponentName: &daprt.MockWorkflow{},
	}

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No workflow component provided in resume request",
			workflowComponent: "",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrNoOrMissingWorkflowComponent,
		},
		{
			testName:          "workflow component does not exist in resume request",
			workflowComponent: "fakeWorkflowNotExist",
			instanceID:        fakeInstanceID,
			expectedError:     messages.ErrWorkflowComponentDoesNotExist.WithFormat("fakeWorkflowNotExist"),
		},
		{
			testName:          "No instance ID provided in resume request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "Resume for this instance throws error",
			workflowComponent: fakeComponentName,
			instanceID:        daprt.ErrorInstanceId,
			expectedError:     messages.ErrResumeWorkflow.WithFormat(daprt.ErrorInstanceId, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in resume request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &UniversalAPI{
		Logger:             logger.NewLogger("test"),
		Resiliency:         resiliency.New(nil),
		WorkflowComponents: fakeWorkflows,
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.ResumeWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.ResumeWorkflowAlpha1(context.Background(), req)

			if tt.expectedError == nil {
				assert.NoError(t, err)
			} else if assert.Error(t, err) {
				assert.Equal(t, tt.expectedError, err)
			}
		})
	}
}
