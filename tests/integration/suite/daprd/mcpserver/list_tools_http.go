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
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"

	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
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
	suite.Register(new(listToolsHTTP))
}

// listToolsHTTP verifies that the dapr.internal.mcp.<name>.ListTools workflow returns
// the correct tool definitions when the MCPServer uses the streamable_http transport.
type listToolsHTTP struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *listToolsHTTP) Setup(t *testing.T) []framework.Option {
	// Build a minimal MCP server with two tools.
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-weather-server", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Get current weather for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "sunny, 72°F in " + args.City}},
		}, struct{}{}, nil
	})
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_forecast",
		Description: "Get weather forecast for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "partly cloudy tomorrow in " + args.City}},
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

func (s *listToolsHTTP) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("ListTools via streamable_http returns expected tools", func(t *testing.T) {
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("weather"), map[string]any{})

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result wfv1.ListMCPToolsResponse
		require.NoError(t, protojson.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))

		names := make([]string, len(result.GetTools()))
		for i, tool := range result.GetTools() {
			names[i] = tool.GetName()
		}
		assert.ElementsMatch(t, []string{"get_weather", "get_forecast"}, names)
	})
}
