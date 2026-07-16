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
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(failureoutput))
}

// failureoutput asserts the Get workflow API contract for a FAILED workflow:
// the failure is reported through the dedicated dapr.workflow.failure.*
// properties, and the failure message is NOT also surfaced as
// dapr.workflow.output. A failed workflow has no successful output, so the
// output property must be absent rather than carry a copy of the error.
type failureoutput struct {
	workflow *workflow.Workflow
}

func (f *failureoutput) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *failureoutput) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	client := f.workflow.BackendClient(t, ctx)

	const activityErr = "activity failed on purpose"

	f.workflow.Registry().AddActivityN("FailingActivity", func(task.ActivityContext) (any, error) {
		return nil, errors.New(activityErr)
	})
	f.workflow.Registry().AddWorkflowN("FailingWorkflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.CallActivity("FailingActivity").Await(nil)
	})

	id, err := client.ScheduleNewWorkflow(ctx, "FailingWorkflow", api.WithInput("input value"))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, meta.GetRuntimeStatus())

	t.Run("client", func(t *testing.T) {
		meta, err := client.FetchWorkflowMetadata(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, meta.GetFailureDetails())
		assert.Contains(t, meta.GetFailureDetails().GetErrorMessage(), activityErr)
		// A failed workflow has no successful output.
		assert.Empty(t, meta.GetOutput().GetValue())
	})

	t.Run("grpc", func(t *testing.T) {
		gclient := f.workflow.GRPCClient(t, ctx)
		resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
			InstanceId:        string(id),
			WorkflowComponent: "dapr",
		})
		require.NoError(t, err)
		assert.Equal(t, "FAILED", resp.GetRuntimeStatus())

		props := resp.GetProperties()
		assert.Contains(t, props["dapr.workflow.failure.error_message"], activityErr)
		assert.NotEmpty(t, props["dapr.workflow.failure.error_type"])

		// The failure message must be reported via the failure property only,
		// never copied into the workflow output.
		_, hasOutput := props["dapr.workflow.output"]
		assert.False(t, hasOutput, "dapr.workflow.output must be absent for a failed workflow, got %q", props["dapr.workflow.output"])
	})

	t.Run("http", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx,
			http.MethodGet,
			fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr/%s", f.workflow.Dapr().HTTPAddress(), id),
			nil,
		)
		require.NoError(t, err)

		hresp, err := fclient.HTTP(t).Do(req)
		require.NoError(t, err)

		type wresp struct {
			RuntimeStatus string            `json:"runtimeStatus"`
			Properties    map[string]string `json:"properties"`
		}
		var w wresp
		require.NoError(t, json.NewDecoder(hresp.Body).Decode(&w))
		require.NoError(t, hresp.Body.Close())

		assert.Equal(t, "FAILED", w.RuntimeStatus)
		assert.Contains(t, w.Properties["dapr.workflow.failure.error_message"], activityErr)

		_, hasOutput := w.Properties["dapr.workflow.output"]
		assert.False(t, hasOutput, "dapr.workflow.output must be absent for a failed workflow, got %q", w.Properties["dapr.workflow.output"])
	})
}
