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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"

	daprmcp "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/types"
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
	suite.Register(new(multipleServers))
}

// multipleServers verifies that two MCPServer resources can coexist and each
// routes to its own backend MCP server.
type multipleServers struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *multipleServers) Setup(t *testing.T) []framework.Option {
	// Server A: weather service
	mcpSrvA := mcp.NewServer(&mcp.Implementation{Name: "server-a", Version: "v1"}, nil)
	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrvA, &mcp.Tool{
		Name:        "get_weather",
		Description: "Get weather for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("sunny in %s", args.City)}},
		}, struct{}{}, nil
	})
	mcpProcA := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrvA }, nil),
	))

	// Server B: greeting service
	mcpSrvB := mcp.NewServer(&mcp.Implementation{Name: "server-b", Version: "v1"}, nil)
	type nameInput struct {
		Name string `json:"name"`
	}
	mcp.AddTool(mcpSrvB, &mcp.Tool{
		Name:        "greet",
		Description: "Return a greeting",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args nameInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("hello %s", args.Name)}},
		}, struct{}{}, nil
	})
	mcpProcB := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrvB }, nil),
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
		daprd.WithResourceFiles(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpProcA.Port()),
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: greeter
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpProcB.Port()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpProcA, mcpProcB, s.daprd),
	}
}

func (s *multipleServers) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("CallTool on weather server returns weather", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "weather",
			"toolName":          "get_weather",
			"arguments":     map[string]any{"city": "Austin"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.weather.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].Text, "Austin"))
	})

	t.Run("CallTool on greeter server returns greeting", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "greeter",
			"toolName":          "greet",
			"arguments":     map[string]any{"name": "dapr"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.greeter.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].Text, "dapr"))
	})

	t.Run("ListTools returns different tools per server", func(t *testing.T) {
		// Weather server
		weatherID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.weather.ListTools", map[string]any{"mcpServerName": "weather"})
		weatherMeta, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(weatherID), api.WithFetchPayloads(true))
		require.NoError(t, err)

		var weatherResult daprmcp.ListToolsResult
		require.NoError(t, json.Unmarshal([]byte(weatherMeta.GetOutput().GetValue()), &weatherResult))
		weatherNames := make([]string, len(weatherResult.Tools))
		for i, tool := range weatherResult.Tools {
			weatherNames[i] = tool.Name
		}
		assert.Contains(t, weatherNames, "get_weather")
		assert.NotContains(t, weatherNames, "greet")

		// Greeter server
		greeterID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.internal.mcp.greeter.ListTools", map[string]any{"mcpServerName": "greeter"})
		greeterMeta, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(greeterID), api.WithFetchPayloads(true))
		require.NoError(t, err)

		var greeterResult daprmcp.ListToolsResult
		require.NoError(t, json.Unmarshal([]byte(greeterMeta.GetOutput().GetValue()), &greeterResult))
		greeterNames := make([]string, len(greeterResult.Tools))
		for i, tool := range greeterResult.Tools {
			greeterNames[i] = tool.Name
		}
		assert.Contains(t, greeterNames, "greet")
		assert.NotContains(t, greeterNames, "get_weather")
	})
}
