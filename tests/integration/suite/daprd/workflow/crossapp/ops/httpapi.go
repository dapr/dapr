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

package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(httpapi))
}

// httpapi exercises cross-app workflow operations through the Dapr HTTP API
// using the appID query parameter: app0's HTTP endpoint targets workflows
// hosted on app1.
type httpapi struct {
	workflow *workflow.Workflow
}

func (h *httpapi) Setup(t *testing.T) []framework.Option {
	h.workflow = workflow.New(t, workflow.WithDaprds(2))
	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *httpapi) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	h.workflow.RegistryN(1).AddWorkflowN("HTTPOpsWF", func(wctx *task.WorkflowContext) (any, error) {
		var payload string
		if err := wctx.WaitForSingleEvent("Finish", time.Hour).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	target := h.workflow.BackendClientN(t, ctx, 1)
	targetAppID := h.workflow.DaprN(1).AppID()

	httpClient := fclient.HTTP(t)
	baseURL := fmt.Sprintf("http://%s/v1.0-beta1/workflows/dapr", h.workflow.Dapr().HTTPAddress())
	appIDQuery := "?appID=" + targetAppID

	post := func(t *testing.T, url, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(respBody)
	}

	waitTargetStatus := func(t *testing.T, id string, status api.OrchestrationStatus) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			meta, err := target.FetchWorkflowMetadata(ctx, api.InstanceID(id))
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, status, meta.GetRuntimeStatus())
		}, time.Second*30, time.Millisecond*10)
	}

	const id = "ops-http-1"

	code, body := post(t, baseURL+"/HTTPOpsWF/start"+appIDQuery+"&instanceID="+id, "{}")
	require.Equalf(t, http.StatusAccepted, code, "start failed: %s", body)
	var out struct {
		InstanceID string `json:"instanceID"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	require.Equal(t, id, out.InstanceID)
	waitTargetStatus(t, id, api.RUNTIME_STATUS_RUNNING)

	// Cross-app get over HTTP.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/"+id+appIDQuery, nil)
	require.NoError(t, err)
	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	getBody, err := io.ReadAll(resp.Body)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "get failed: %s", getBody)
	var state struct {
		RuntimeStatus string `json:"runtimeStatus"`
		WorkflowName  string `json:"workflowName"`
	}
	require.NoError(t, json.Unmarshal(getBody, &state))
	assert.Equal(t, "RUNNING", state.RuntimeStatus)
	assert.Equal(t, "HTTPOpsWF", state.WorkflowName)

	code, body = post(t, baseURL+"/"+id+"/pause"+appIDQuery, "")
	require.Equalf(t, http.StatusAccepted, code, "pause failed: %s", body)
	waitTargetStatus(t, id, api.RUNTIME_STATUS_SUSPENDED)

	code, body = post(t, baseURL+"/"+id+"/resume"+appIDQuery, "")
	require.Equalf(t, http.StatusAccepted, code, "resume failed: %s", body)
	waitTargetStatus(t, id, api.RUNTIME_STATUS_RUNNING)

	code, body = post(t, baseURL+"/"+id+"/raiseEvent/Finish"+appIDQuery, `"finished"`)
	require.Equalf(t, http.StatusAccepted, code, "raise event failed: %s", body)
	waitTargetStatus(t, id, api.RUNTIME_STATUS_COMPLETED)

	code, body = post(t, baseURL+"/"+id+"/purge"+appIDQuery, "")
	require.Equalf(t, http.StatusAccepted, code, "purge failed: %s", body)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err := target.FetchWorkflowMetadata(ctx, api.InstanceID(id))
		assert.ErrorIs(c, err, api.ErrInstanceNotFound)
	}, time.Second*30, time.Millisecond*10)

	// Terminate cross-app on a fresh instance.
	const id2 = "ops-http-2"
	code, body = post(t, baseURL+"/HTTPOpsWF/start"+appIDQuery+"&instanceID="+id2, "{}")
	require.Equalf(t, http.StatusAccepted, code, "start failed: %s", body)
	waitTargetStatus(t, id2, api.RUNTIME_STATUS_RUNNING)
	code, body = post(t, baseURL+"/"+id2+"/terminate"+appIDQuery, "")
	require.Equalf(t, http.StatusAccepted, code, "terminate failed: %s", body)
	waitTargetStatus(t, id2, api.RUNTIME_STATUS_TERMINATED)
}
