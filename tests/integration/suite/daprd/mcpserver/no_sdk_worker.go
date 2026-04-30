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

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"


	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(noSDKWorker))
}

// noSDKWorker verifies that MCP internal workflows (ListTools, CallTool)
// execute successfully even when no external gRPC workflow SDK worker is
// connected. The in-process executor and EnsureActorsRegistered path must
// handle actor type registration without a GetWorkItems stream.
type noSDKWorker struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *noSDKWorker) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-no-sdk", Version: "v1"}, nil)

	type echoInput struct {
		Message string `json:"message"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes back the message",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args echoInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Message}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	// No --app-port, no app process — no gRPC SDK worker will connect.
	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: echo-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, mcpSrvProc, s.daprd),
	}
}

func (s *noSDKWorker) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("ListTools works without gRPC SDK worker", func(t *testing.T) {
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("echo-server"), nil)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.ListMCPToolsResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		require.Len(t, result.GetTools(), 1)
		assert.Equal(t, "echo", result.GetTools()[0].GetName())
	})

	t.Run("CallTool works without gRPC SDK worker", func(t *testing.T) {
		input := map[string]any{
			"toolName":  "echo",
			"arguments": map[string]any{"message": "hello-no-sdk"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("echo-server", "echo"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.GetIsError())
		require.NotEmpty(t, result.GetContent())
		assert.True(t, strings.Contains(result.GetContent()[0].GetText().GetText(), "hello-no-sdk"))
	})
}

// wfStatus is a shared type for polling workflow status via HTTP.
type wfStatus struct {
	RuntimeStatus string            `json:"runtimeStatus"`
	Properties    map[string]string `json:"properties"`
}

// pollWorkflowCompletion polls the workflow status endpoint until the workflow
// reaches a terminal state or the timeout expires.
func pollWorkflowCompletion(ctx context.Context, t *testing.T, httpClient *http.Client, httpPort int, instanceID string, timeout time.Duration) wfStatus {
	t.Helper()

	statusURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s", httpPort, instanceID)

	var status wfStatus
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
		if !assert.NoError(c, err) {
			return
		}
		resp, err := httpClient.Do(req)
		if !assert.NoError(c, err) {
			return
		}
		defer resp.Body.Close()
		if !assert.NoError(c, json.NewDecoder(resp.Body).Decode(&status)) {
			return
		}
		assert.True(c,
			status.RuntimeStatus == protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED.String() ||
				status.RuntimeStatus == protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED.String(),
			"expected terminal status, got %s", status.RuntimeStatus)
	}, timeout, 10*time.Millisecond)

	return status
}
