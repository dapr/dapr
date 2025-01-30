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
	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	"github.com/dapr/kit/logger"
)

const (
	fakeComponentName = "fakeWorkflowComponent"
	fakeInstanceID    = "fake-instance-ID__123"
)

func TestStartWorkflowAPI(t *testing.T) {
	fakeWorkflowName := "fakeWorkflow"

	testCases := []struct {
		testName          string
		workflowComponent string
		workflowName      string
		instanceID        string
		expectedError     error
	}{
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

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:     logger.NewLogger("test"),
		resiliency: resiliency.New(nil),
		workflowEngine: fake.New().WithClient(func() workflows.Workflow {
			return fake.NewClient().WithGet(func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
				return &workflows.StateResponse{
					Workflow: &workflows.WorkflowState{
						RuntimeStatus: "RUNNING",
					},
				}, nil
			})
		}),
		actors: actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.StartWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				WorkflowName:      tt.workflowName,
			}
			_, err := fakeAPI.StartWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestGetWorkflowAPI(t *testing.T) {
	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No instance ID provided in get request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "All is well in get request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.GetWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestTerminateWorkflowAPI(t *testing.T) {
	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No instance ID provided in terminate request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "All is well in terminate request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:     logger.NewLogger("test"),
		resiliency: resiliency.New(nil),
		workflowEngine: fake.New().WithClient(func() workflows.Workflow {
			return fake.NewClient().WithGet(func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
				return &workflows.StateResponse{
					Workflow: &workflows.WorkflowState{
						RuntimeStatus: "TERMINATED",
					},
				}, nil
			})
		}),
		actors: actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.TerminateWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.TerminateWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestRaiseEventWorkflowApi(t *testing.T) {
	fakeEventName := "fake_event_name"

	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		eventName         string
		expectedError     error
	}{
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
			testName:          "All is well in raise event request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
			eventName:         fakeEventName,
		},
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.RaiseEventWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				EventName:         tt.eventName,
				EventData:         []byte("fake_input"),
			}
			_, err := fakeAPI.RaiseEventWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestPauseWorkflowApi(t *testing.T) {
	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No instance ID provided in pause request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "All is well in pause request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:     logger.NewLogger("test"),
		resiliency: resiliency.New(nil),
		workflowEngine: fake.New().WithClient(func() workflows.Workflow {
			return fake.NewClient().WithGet(func(ctx context.Context, req *workflows.GetRequest) (*workflows.StateResponse, error) {
				return &workflows.StateResponse{
					Workflow: &workflows.WorkflowState{
						RuntimeStatus: "SUSPENDED",
					},
				}, nil
			})
		}),
		actors: actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.PauseWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.PauseWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

func TestResumeWorkflowApi(t *testing.T) {
	testCases := []struct {
		testName          string
		workflowComponent string
		instanceID        string
		expectedError     error
	}{
		{
			testName:          "No instance ID provided in resume request",
			workflowComponent: fakeComponentName,
			instanceID:        "",
			expectedError:     messages.ErrMissingOrEmptyInstance,
		},
		{
			testName:          "All is well in resume request",
			workflowComponent: fakeComponentName,
			instanceID:        fakeInstanceID,
		},
	}

	// Setup universal dapr API
	fakeAPI := &Universal{
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.ResumeWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.ResumeWorkflow(context.Background(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}
