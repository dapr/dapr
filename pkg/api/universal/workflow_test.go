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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/fake"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
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
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.StartWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
				WorkflowName:      tt.workflowName,
			}
			_, err := fakeAPI.StartWorkflow(t.Context(), req)

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
			_, err := fakeAPI.GetWorkflow(t.Context(), req)

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
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.TerminateWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.TerminateWorkflow(t.Context(), req)

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
			_, err := fakeAPI.RaiseEventWorkflow(t.Context(), req)

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
		logger:         logger.NewLogger("test"),
		resiliency:     resiliency.New(nil),
		workflowEngine: fake.New(),
		actors:         actorsfake.New(),
	}

	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.PauseWorkflowRequest{
				WorkflowComponent: tt.workflowComponent,
				InstanceId:        tt.instanceID,
			}
			_, err := fakeAPI.PauseWorkflow(t.Context(), req)

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
			_, err := fakeAPI.ResumeWorkflow(t.Context(), req)

			if tt.expectedError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.expectedError)
			}
		})
	}
}

// TestWorkflowInstanceNotFoundError verifies that the Terminate and Purge
// handlers format ErrWorkflowInstanceNotFound with a single argument. The
// message template has one verb, so passing the sentinel error as an extra
// argument used to append a "%!(EXTRA ...)" formatting marker to the message.
// ErrorIs cannot catch this because APIError.Is ignores the message, so the
// exact message is asserted here.
func TestWorkflowInstanceNotFoundError(t *testing.T) {
	expectedMessage := messages.ErrWorkflowInstanceNotFound.WithFormat(fakeInstanceID).Message()

	fakeAPI := &Universal{
		logger:     logger.NewLogger("test"),
		resiliency: resiliency.New(nil),
		workflowEngine: fake.New().WithClient(func() backend.TaskHubClient {
			return fake.NewClient().
				WithTerminateWorkflow(func(ctx context.Context, id api.InstanceID, opts ...api.TerminateOptions) error {
					return api.ErrInstanceNotFound
				}).
				WithPurgeWorkflowState(func(ctx context.Context, id api.InstanceID, opts ...api.PurgeOptions) error {
					return api.ErrInstanceNotFound
				})
		}),
		actors: actorsfake.New(),
	}

	t.Run("Terminate returns the not-found error without extra formatting", func(t *testing.T) {
		_, err := fakeAPI.TerminateWorkflow(t.Context(), &runtimev1pb.TerminateWorkflowRequest{
			WorkflowComponent: fakeComponentName,
			InstanceId:        fakeInstanceID,
		})
		require.ErrorIs(t, err, messages.ErrWorkflowInstanceNotFound)
		var apiErr messages.APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, expectedMessage, apiErr.Message())
		require.NotContains(t, apiErr.Message(), "%!(EXTRA")
	})

	t.Run("Purge returns the not-found error without extra formatting", func(t *testing.T) {
		_, err := fakeAPI.PurgeWorkflow(t.Context(), &runtimev1pb.PurgeWorkflowRequest{
			WorkflowComponent: fakeComponentName,
			InstanceId:        fakeInstanceID,
		})
		require.ErrorIs(t, err, messages.ErrWorkflowInstanceNotFound)
		var apiErr messages.APIError
		require.ErrorAs(t, err, &apiErr)
		require.Equal(t, expectedMessage, apiErr.Message())
		require.NotContains(t, apiErr.Message(), "%!(EXTRA")
	})
}

func newGetWorkflowAPI(metadata *backend.WorkflowMetadata, metadataErr error) *Universal {
	return &Universal{
		logger:     logger.NewLogger("test"),
		resiliency: resiliency.New(nil),
		workflowEngine: fake.New().WithClient(func() backend.TaskHubClient {
			return fake.NewClient().WithFetchWorkflowMetadata(func(ctx context.Context, id api.InstanceID, opts ...api.FetchWorkflowMetadataOptions) (*backend.WorkflowMetadata, error) {
				return metadata, metadataErr
			})
		}),
		actors: actorsfake.New(),
	}
}

// TestGetWorkflowOutputOnFailure verifies that GetWorkflow only exposes
// dapr.workflow.output for workflows that did not fail. On failure
// durabletask-go stores the failure message in the completed-event result
// (surfaced as Output), but that error is already reported via
// dapr.workflow.failure.*, so it must not leak into output.
func TestGetWorkflowOutputOnFailure(t *testing.T) {
	const errMsg = "Task 'SuperSlowActivity' (#0) failed with an unhandled exception: boom"

	tests := map[string]struct {
		metadata       *backend.WorkflowMetadata
		expectOutput   bool
		expectedOutput string
	}{
		"failed workflow omits output": {
			metadata: &backend.WorkflowMetadata{
				RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED,
				Output:        wrapperspb.String(errMsg),
				FailureDetails: &protos.TaskFailureDetails{
					ErrorType:    "Exception",
					ErrorMessage: errMsg,
				},
			},
			expectOutput: false,
		},
		"completed workflow keeps output": {
			metadata: &backend.WorkflowMetadata{
				RuntimeStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				Output:        wrapperspb.String(`{"result":42}`),
			},
			expectOutput:   true,
			expectedOutput: `{"result":42}`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeAPI := newGetWorkflowAPI(tc.metadata, nil)

			res, err := fakeAPI.GetWorkflow(t.Context(), &runtimev1pb.GetWorkflowRequest{InstanceId: fakeInstanceID})
			require.NoError(t, err)

			output, ok := res.GetProperties()["dapr.workflow.output"]
			if tc.expectOutput {
				assert.True(t, ok, "expected dapr.workflow.output to be set")
				assert.Equal(t, tc.expectedOutput, output)
			} else {
				assert.False(t, ok, "dapr.workflow.output must be omitted for a failed workflow")
				// The failure message is still surfaced through the dedicated field.
				assert.Equal(t, errMsg, res.GetProperties()["dapr.workflow.failure.error_message"])
			}
		})
	}
}

func TestGetWorkflowRuntimeStatus(t *testing.T) {
	tests := map[string]struct {
		status   protos.OrchestrationStatus
		expected string
	}{
		"running":   {protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, "RUNNING"},
		"completed": {protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, "COMPLETED"},
		"failed":    {protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, "FAILED"},
		"suspended": {protos.OrchestrationStatus_ORCHESTRATION_STATUS_SUSPENDED, "SUSPENDED"},
		"unknown":   {protos.OrchestrationStatus(99), "UNKNOWN"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakeAPI := newGetWorkflowAPI(&backend.WorkflowMetadata{RuntimeStatus: tc.status}, nil)

			res, err := fakeAPI.GetWorkflow(t.Context(), &runtimev1pb.GetWorkflowRequest{InstanceId: fakeInstanceID})
			require.NoError(t, err)
			assert.Equal(t, tc.expected, res.GetRuntimeStatus())
			assert.Equal(t, fakeInstanceID, res.GetInstanceId())
			assert.NotNil(t, res.GetProperties())
		})
	}
}

// TestGetWorkflowNotFound verifies that a missing instance results in a nil
// error and a response carrying only the requested instance ID.
func TestGetWorkflowNotFound(t *testing.T) {
	fakeAPI := newGetWorkflowAPI(nil, api.ErrInstanceNotFound)

	res, err := fakeAPI.GetWorkflow(t.Context(), &runtimev1pb.GetWorkflowRequest{InstanceId: fakeInstanceID})
	require.NoError(t, err)
	assert.Equal(t, fakeInstanceID, res.GetInstanceId())
	assert.Empty(t, res.GetWorkflowName())
	assert.Empty(t, res.GetRuntimeStatus())
}
