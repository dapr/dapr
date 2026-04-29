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
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(callToolPerToolWorkflow))
}

// callToolPerToolWorkflow verifies that each MCP tool is registered as its own
// workflow with the name dapr.internal.mcp.<server>.CallTool.<tool>.
// This enables per-tool observability in workflow traces.
//
// Tests:
// 1. Calling a specific tool by its per-tool workflow name succeeds.
// 2. Calling a different tool on the same server uses a different workflow name.
// 3. Calling with a non-existent tool name (workflow not registered) fails.
// 4. The tool name is extracted from the workflow name, not the input.
type callToolPerToolWorkflow struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *callToolPerToolWorkflow) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "per-tool-test", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Return weather for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Weather in %s: sunny", args.City)}},
		}, struct{}{}, nil
	})

	type nameInput struct {
		Name string `json:"name"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "greet",
		Description: "Greet someone",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args nameInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Hello, %s!", args.Name)}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: multi-tool
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

func (s *callToolPerToolWorkflow) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("per-tool workflow name calls correct tool", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{"city": "Portland"},
		}
		// Use the per-tool workflow name: dapr.internal.mcp.multi-tool.CallTool.get_weather
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("multi-tool", "get_weather"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].GetText().GetText(), "Portland"))
	})

	t.Run("different tool uses different workflow name", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{"name": "Dapr"},
		}
		// Use the greet tool's workflow name
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("multi-tool", "greet"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].GetText().GetText(), "Hello, Dapr!"))
	})

	t.Run("non-existent tool workflow name fails as not registered", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{},
		}
		// Try a tool that doesn't exist — the workflow should not be registered
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("multi-tool", "nonexistent_tool"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, status.RuntimeStatus,
			"workflow for non-existent tool should fail as not registered")
	})

	t.Run("tool name extracted from workflow name not input", func(t *testing.T) {
		// Pass a WRONG tool_name in the input but use the correct workflow name.
		// The orchestrator should use the tool name from the workflow name (authoritative).
		input := map[string]any{
			"toolName":  "wrong_tool_name",
			"arguments": map[string]any{"city": "Seattle"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("multi-tool", "get_weather"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus,
			"should succeed using tool name from workflow name, ignoring input.toolName")

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.IsError)
		assert.True(t, strings.Contains(result.Content[0].GetText().GetText(), "Seattle"),
			"expected weather result from get_weather, got: %s", result.Content[0].GetText().GetText())
	})

	t.Run("ListTools still returns all tools", func(t *testing.T) {
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("multi-tool"), nil)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.ListMCPToolsResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.Len(t, result.Tools, 2, "expected both get_weather and greet tools")

		toolNames := make([]string, len(result.Tools))
		for i, tool := range result.Tools {
			toolNames[i] = tool.Name
		}
		assert.Contains(t, toolNames, "get_weather")
		assert.Contains(t, toolNames, "greet")
	})
}
