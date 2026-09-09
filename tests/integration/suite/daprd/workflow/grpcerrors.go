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

package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpcerrors))
}

// grpcerrors covers the rich gRPC error responses produced by the Workflow
// API's error standardization (#7487): each of these fails validation before
// reaching the workflow engine, so no workflow needs to actually run.
type grpcerrors struct {
	workflow *workflow.Workflow
}

func (g *grpcerrors) Setup(t *testing.T) []framework.Option {
	g.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *grpcerrors) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	client := g.workflow.GRPCClient(t, ctx)

	requireErrorInfo := func(t *testing.T, err error, code grpcCodes.Code, message string, reason string) {
		t.Helper()

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, code, s.Code())
		require.Equal(t, message, s.Message())

		require.Len(t, s.Details(), 1)
		errInfo, ok := s.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, reason, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	}

	t.Run("start workflow missing name", func(t *testing.T) {
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			InstanceId:        "grpcerrors-start-missing-name",
			WorkflowComponent: "dapr",
		})
		requireErrorInfo(t, err, grpcCodes.InvalidArgument,
			"workflow name is not configured", "DAPR_WORKFLOW_NAME_MISSING")
	})

	t.Run("start workflow invalid app ID", func(t *testing.T) {
		appID := "not a valid app id"
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			InstanceId:        "grpcerrors-start-bad-appid",
			WorkflowComponent: "dapr",
			WorkflowName:      "someWorkflow",
			AppId:             &appID,
		})
		requireErrorInfo(t, err, grpcCodes.InvalidArgument,
			"workflow app ID 'not a valid app id' is invalid: only alphanumeric, dash and underscore characters are allowed",
			"DAPR_WORKFLOW_APP_ID_INVALID")
	})

	// Invalid characters are only checked on creation (see validateInstanceID's
	// isCreate parameter), so this is exercised via StartWorkflow rather than
	// an action on an existing instance.
	t.Run("start workflow invalid instance ID", func(t *testing.T) {
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			InstanceId:        "not valid!",
			WorkflowComponent: "dapr",
			WorkflowName:      "someWorkflow",
		})
		requireErrorInfo(t, err, grpcCodes.InvalidArgument,
			"workflow instance ID 'not valid!' is invalid: only alphanumeric and underscore characters are allowed",
			"DAPR_WORKFLOW_INSTANCE_ID_INVALID")
	})

	t.Run("pause workflow missing instance ID", func(t *testing.T) {
		_, err := client.PauseWorkflowBeta1(ctx, &rtv1.PauseWorkflowRequest{
			WorkflowComponent: "dapr",
		})
		requireErrorInfo(t, err, grpcCodes.InvalidArgument,
			"no instance ID was provided", "DAPR_WORKFLOW_INSTANCE_ID_MISSING")
	})

	t.Run("raise event missing event name", func(t *testing.T) {
		_, err := client.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
			InstanceId:        "grpcerrors-raise-event-missing-name",
			WorkflowComponent: "dapr",
		})
		requireErrorInfo(t, err, grpcCodes.InvalidArgument,
			"missing workflow event name", "DAPR_WORKFLOW_EVENT_NAME_MISSING")
	})
}
