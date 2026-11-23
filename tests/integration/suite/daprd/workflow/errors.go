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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(errorsuite))
}

type errorsuite struct {
	workflow *workflow.Workflow
}

const (
	errInfoType      = "type.googleapis.com/google.rpc.ErrorInfo"
	resourceInfoType = "type.googleapis.com/google.rpc.ResourceInfo"
)

func (e *errorsuite) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *errorsuite) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	httpClient := fclient.HTTP(t)

	request := func(t *testing.T, method, url string) map[string]any {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(body, &data))
		return data
	}

	// Covers errors.WorkflowInstanceIDInvalid: the instance ID is part of the
	// request, so the response carries a ResourceInfo detail ahead of the
	// ErrorInfo one.
	t.Run("start with an invalid instance ID", func(t *testing.T) {
		instanceID := "invalid$ID"
		url := fmt.Sprintf("http://%s/v1.0/workflows/dapr/some-workflow/start?instanceID=%s",
			e.workflow.Dapr().HTTPAddress(), instanceID)

		data := request(t, http.MethodPost, url)

		assert.Equal(t, "ERR_INSTANCE_ID_INVALID", data["errorCode"])
		assert.Equal(t,
			fmt.Sprintf("workflow instance ID '%s' is invalid: only alphanumeric and underscore characters are allowed", instanceID),
			data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok)
		require.Len(t, details, 2)

		resourceInfo, ok := details[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, resourceInfoType, resourceInfo["@type"])
		assert.Equal(t, "workflow", resourceInfo["resource_type"])
		assert.Equal(t, instanceID, resourceInfo["resource_name"])

		errorInfo, ok := details[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, errInfoType, errorInfo["@type"])
		assert.Equal(t, "dapr.io", errorInfo["domain"])
		assert.Equal(t, "DAPR_WORKFLOW_INSTANCE_ID_INVALID", errorInfo["reason"])
	})

	// Covers errors.WorkflowInstanceNotFound. A terminate only reaches the
	// durabletask client once a workflow worker is connected, so register a
	// workflow first; without one the request fails earlier with
	// ERR_TERMINATE_WORKFLOW.
	t.Run("terminate an instance that does not exist", func(t *testing.T) {
		reg := e.workflow.Registry()
		reg.AddWorkflowN("dummy", func(*task.WorkflowContext) (any, error) {
			return nil, nil
		})
		e.workflow.ConnectWorker(t, ctx, reg)
		e.workflow.WaitForConnectedWorkers(t, ctx, 1)

		instanceID := "not-found"
		url := fmt.Sprintf("http://%s/v1.0/workflows/dapr/%s/terminate",
			e.workflow.Dapr().HTTPAddress(), instanceID)

		data := request(t, http.MethodPost, url)

		assert.Equal(t, "ERR_INSTANCE_ID_NOT_FOUND", data["errorCode"])
		assert.Equal(t,
			fmt.Sprintf("unable to find workflow with the provided instance ID: %s", instanceID),
			data["message"])

		details, ok := data["details"].([]any)
		require.True(t, ok)
		require.Len(t, details, 2)

		resourceInfo, ok := details[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, resourceInfoType, resourceInfo["@type"])
		assert.Equal(t, "workflow", resourceInfo["resource_type"])
		assert.Equal(t, instanceID, resourceInfo["resource_name"])

		errorInfo, ok := details[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, errInfoType, errorInfo["@type"])
		assert.Equal(t, "dapr.io", errorInfo["domain"])
		assert.Equal(t, "DAPR_WORKFLOW_INSTANCE_NOT_FOUND", errorInfo["reason"])
	})
}
