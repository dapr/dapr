/*
Copyright 2025 The Dapr Authors
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
	suite.Register(new(errors))
}

type errors struct {
	workflow *workflow.Workflow
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	client := e.workflow.GRPCClient(t, ctx)

	assertErrorInfo := func(t *testing.T, err error, code grpcCodes.Code, message string, reason string, metadata map[string]string) {
		t.Helper()

		statusErr, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, code, statusErr.Code())
		require.Equal(t, message, statusErr.Message())

		require.Len(t, statusErr.Details(), 1)
		errInfo, ok := statusErr.Details()[0].(*errdetails.ErrorInfo)
		require.True(t, ok)
		require.Equal(t, reason, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		if len(metadata) == 0 {
			require.Empty(t, errInfo.GetMetadata())
		} else {
			require.Equal(t, metadata, errInfo.GetMetadata())
		}
	}

	t.Run("workflow name missing", func(t *testing.T) {
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "",
			InstanceId:        "instance-id",
		})
		require.Error(t, err)

		assertErrorInfo(
			t,
			err,
			grpcCodes.InvalidArgument,
			"workflow name is not configured",
			"ERR_WORKFLOW_NAME_MISSING",
			nil,
		)
	})

	t.Run("instance id missing", func(t *testing.T) {
		_, err := client.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "",
		})
		require.Error(t, err)

		assertErrorInfo(
			t,
			err,
			grpcCodes.InvalidArgument,
			"no instance ID was provided",
			"ERR_INSTANCE_ID_PROVIDED_MISSING",
			nil,
		)
	})

	t.Run("instance id invalid", func(t *testing.T) {
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "workflow",
			InstanceId:        "invalid#id",
		})
		require.Error(t, err)

		assertErrorInfo(
			t,
			err,
			grpcCodes.InvalidArgument,
			"workflow instance ID 'invalid#id' is invalid: only alphanumeric and underscore characters are allowed",
			"ERR_INSTANCE_ID_INVALID",
			map[string]string{"instanceId": "invalid#id"},
		)
	})

	t.Run("instance id too long", func(t *testing.T) {
		longID := "this_is_a_very_long_instance_id_that_is_longer_than_64_characters_and_therefore_should_not_be_allowed"
		_, err := client.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
			WorkflowComponent: "dapr",
			WorkflowName:      "workflow",
			InstanceId:        longID,
		})
		require.Error(t, err)

		assertErrorInfo(
			t,
			err,
			grpcCodes.InvalidArgument,
			"workflow instance ID exceeds the max length of 64 characters",
			"ERR_INSTANCE_ID_TOO_LONG",
			map[string]string{"maxLength": "64"},
		)
	})

	t.Run("event name missing", func(t *testing.T) {
		_, err := client.RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
			WorkflowComponent: "dapr",
			InstanceId:        "instance-id",
			EventName:         "",
		})
		require.Error(t, err)

		assertErrorInfo(
			t,
			err,
			grpcCodes.InvalidArgument,
			"missing workflow event name",
			"ERR_WORKFLOW_EVENT_NAME_MISSING",
			nil,
		)
	})
}
