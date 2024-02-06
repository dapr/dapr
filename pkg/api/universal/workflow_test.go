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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	fakeComponentName = "fakeWorkflowComponent"
	fakeInstanceID    = "fake-instance-ID__123"
)

func TestStartWorkflowBeta1API(t *testing.T) {
	fakeWorkflowName := "fakeWorkflow"

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
			instanceID:        daprt.ErrorInstanceID,
			expectedError:     messages.ErrStartWorkflow.WithFormat(fakeWorkflowName, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "No instance ID provided in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        "",
		},
		{
			testName:          "All is well in start request",
			workflowComponent: fakeComponentName,
			workflowName:      fakeWorkflowName,
			instanceID:        fakeInstanceID,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.StartWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				WorkflowName:      tt.workflowName,
			}
			_, err := fakeAPI.StartWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestGetWorkflowBeta1API(t *testing.T) {
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
			instanceID:        daprt.ErrorInstanceID,
			expectedError:     messages.ErrWorkflowGetResponse.WithFormat(daprt.ErrorInstanceID, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in get request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.GetWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestTerminateWorkflowBeta1API(t *testing.T) {
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
			instanceID:        daprt.ErrorInstanceID,
			expectedError:     messages.ErrTerminateWorkflow.WithFormat(daprt.ErrorInstanceID, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in terminate request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.TerminateWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.TerminateWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestRaiseEventWorkflowBeta1Api(t *testing.T) {
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
			instanceID:        daprt.ErrorInstanceID,
			eventName:         fakeEventName,
			expectedError:     messages.ErrRaiseEventWorkflow.WithFormat(daprt.ErrorInstanceID, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in raise event request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
			eventName:         fakeEventName,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.RaiseEventWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				EventName:         tt.eventName,
				EventData:         []byte("fake_input"),
			}
			_, err := fakeAPI.RaiseEventWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestPauseWorkflowBeta1Api(t *testing.T) {
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
			instanceID:        daprt.ErrorInstanceID,
			expectedError:     messages.ErrPauseWorkflow.WithFormat(daprt.ErrorInstanceID, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in pause request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.PauseWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.PauseWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestResumeWorkflowBeta1Api(t *testing.T) {
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
			instanceID:        daprt.ErrorInstanceID,
			expectedError:     messages.ErrResumeWorkflow.WithFormat(daprt.ErrorInstanceID, daprt.ErrFakeWorkflowComponentError),
		},
		{
			testName:          "All is well in resume request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	compStore := compstore.New()
	for name, wf := range fakeWorkflows {
		compStore.AddWorkflow(name, wf)
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		compStore:      compStore,
		actorsReadyCh:  make(chan struct{}),
		workflowEngine: getWorkflowEngine(),
	}
	fakeAPI.SetActorsInitDone()

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.ResumeWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.ResumeWorkflowBeta1(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func getWorkflowEngine() *wfengine.WorkflowEngine {
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}
	wfengine := wfengine.NewWorkflowEngine("testAppID", spec, nil)
	wfengine.SetWorkflowEngineReadyDone()
	return wfengine
}
