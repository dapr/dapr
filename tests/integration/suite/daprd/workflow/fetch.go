/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fetch))
}

type fetch struct {
	workflow *workflow.Workflow
}

func (f *fetch) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fetch) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	f.workflow.Registry().AddOrchestratorN("getter", func(ctx *task.OrchestrationContext) (any, error) {
		ctx.SetCustomStatus("my custom status")
		return "return value", nil
	})

	client := f.workflow.BackendClient(t, ctx)

	id, err := client.ScheduleNewOrchestration(ctx, "getter", api.WithInput("input value"))
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	meta, err := client.FetchOrchestrationMetadata(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, `"input value"`, meta.GetInput().GetValue())
	assert.Equal(t, `"return value"`, meta.GetOutput().GetValue())
	assert.Equal(t, `my custom status`, meta.GetCustomStatus().GetValue())

	gclient := f.workflow.GRPCClient(t, ctx)
	resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{
		InstanceId:        string(id),
		WorkflowComponent: "dapr",
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"dapr.workflow.custom_status": "my custom status",
		"dapr.workflow.input":         `"input value"`,
		"dapr.workflow.output":        `"return value"`,
	}, resp.GetProperties())

	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr/%s", f.workflow.Dapr().HTTPAddress(), id),
		nil,
	)
	require.NoError(t, err)

	hresp, err := fclient.HTTP(t).Do(req)
	require.NoError(t, err)

	type wresp struct {
		Properties map[string]string `json:"properties"`
	}
	var w wresp
	require.NoError(t, json.NewDecoder(hresp.Body).Decode(&w))
	require.NoError(t, hresp.Body.Close())

	assert.Equal(t, map[string]string{
		"dapr.workflow.custom_status": "my custom status",
		"dapr.workflow.input":         `"input value"`,
		"dapr.workflow.output":        `"return value"`,
	}, w.Properties)
}
