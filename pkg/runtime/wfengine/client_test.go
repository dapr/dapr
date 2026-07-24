/*
Copyright 2026 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"

	"github.com/dapr/components-contrib/workflows"
	"github.com/dapr/kit/logger"
)

// fakeTaskHubClient is a minimal backend.TaskHubClient whose only behaviour is
// to return a pre-built WorkflowMetadata from FetchWorkflowMetadata. Every
// other method is an unused no-op required to satisfy the interface.
type fakeTaskHubClient struct {
	metadata *backend.WorkflowMetadata
}

func (f *fakeTaskHubClient) FetchWorkflowMetadata(context.Context, api.InstanceID) (*backend.WorkflowMetadata, error) {
	return f.metadata, nil
}

func (f *fakeTaskHubClient) ScheduleNewWorkflow(context.Context, any, ...api.NewWorkflowOptions) (api.InstanceID, error) {
	return api.EmptyInstanceID, nil
}

func (f *fakeTaskHubClient) WaitForWorkflowStart(context.Context, api.InstanceID) (*backend.WorkflowMetadata, error) {
	return nil, nil
}

func (f *fakeTaskHubClient) WaitForWorkflowCompletion(context.Context, api.InstanceID) (*backend.WorkflowMetadata, error) {
	return nil, nil
}

func (f *fakeTaskHubClient) TerminateWorkflow(context.Context, api.InstanceID, ...api.TerminateOptions) error {
	return nil
}

func (f *fakeTaskHubClient) RaiseEvent(context.Context, api.InstanceID, string, ...api.RaiseEventOptions) error {
	return nil
}

func (f *fakeTaskHubClient) SuspendWorkflow(context.Context, api.InstanceID, string) error {
	return nil
}

func (f *fakeTaskHubClient) ResumeWorkflow(context.Context, api.InstanceID, string) error {
	return nil
}

func (f *fakeTaskHubClient) PurgeWorkflowState(context.Context, api.InstanceID, ...api.PurgeOptions) error {
	return nil
}

func (f *fakeTaskHubClient) RerunWorkflowFromEvent(context.Context, api.InstanceID, uint32, ...api.RerunOptions) (api.InstanceID, error) {
	return api.EmptyInstanceID, nil
}

// TestGetOutputOnFailure verifies that Get only exposes dapr.workflow.output for
// workflows that did not fail. On failure durabletask-go stores the failure
// message in the completed-event result (surfaced as Output), but that error is
// already reported via dapr.workflow.failure.*, so it must not leak into output.
func TestGetOutputOnFailure(t *testing.T) {
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
			c := &client{
				logger: logger.NewLogger("test"),
				client: &fakeTaskHubClient{metadata: tc.metadata},
			}

			res, err := c.Get(context.Background(), &workflows.GetRequest{InstanceID: "wf1"})
			require.NoError(t, err)
			require.NotNil(t, res.Workflow)

			output, ok := res.Workflow.Properties["dapr.workflow.output"]
			if tc.expectOutput {
				assert.True(t, ok, "expected dapr.workflow.output to be set")
				assert.Equal(t, tc.expectedOutput, output)
			} else {
				assert.False(t, ok, "dapr.workflow.output must be omitted for a failed workflow")
				// The failure message is still surfaced through the dedicated field.
				assert.Equal(t, errMsg, res.Workflow.Properties["dapr.workflow.failure.error_message"])
			}
		})
	}
}
