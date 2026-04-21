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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callTool))
}

// callTool verifies that the dapr.internal.mcp.<name>.CallTool workflow invokes the
// requested tool and returns its result via the workflow output.
type callTool struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *callTool) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-calltool-server", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Return current conditions for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		text := fmt.Sprintf("Weather in %s: sunny, 72°F", args.City)
		if args.City == "" {
			text = "Weather: sunny, 72°F"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, struct{}{}, nil
	})

	// Serve the StreamableHTTPHandler at root — matching how worker_test.go sets up
	// test servers (httptest.NewServer(handler) with no path suffix in the URL).
	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	appProc := app.New(t)

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpconfig
spec:
  features:
  - name: MCPServerResource
    enabled: true
`),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *callTool) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("CallTool returns tool result with correct content", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "weather",
			"toolName":          "get_weather",
			"arguments":     map[string]any{"city": "Seattle"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.weather.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		var result rtv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))

		assert.False(t, result.IsError, "expected success result")
		require.NotEmpty(t, result.Content)
		assert.Equal(t, "text", result.Content[0].Type)
		assert.True(t, strings.Contains(result.Content[0].Text, "Seattle"),
			"expected tool result to mention Seattle, got: %s", result.Content[0].Text)
	})

	t.Run("CallTool unknown tool name sets isError=true", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "weather",
			"toolName":          "nonexistent_tool",
			"arguments":     map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.weather.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		// The MCP server returns isError=true for unknown tools, which surfaces
		// as a completed workflow with isError=true in the output, NOT a workflow failure.
		var result rtv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.True(t, result.IsError, "expected isError=true for unknown tool")
	})
}
