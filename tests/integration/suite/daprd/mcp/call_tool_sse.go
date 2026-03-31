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

package mcp

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

	daprmcp "github.com/dapr/dapr/pkg/runtime/mcp"
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
	suite.Register(new(callToolSSE))
}

// callToolSSE verifies that CallTool works correctly over the legacy SSE transport.
type callToolSSE struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *callToolSSE) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-sse-calltool-server", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Return current conditions for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		text := fmt.Sprintf("Weather in %s: sunny, 72F", args.City)
		if args.City == "" {
			text = "Weather: sunny, 72F"
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
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
  name: weather-sse
spec:
  endpoint:
    transport: sse
    target:
      url: http://localhost:%d
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *callToolSSE) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("CallTool via SSE transport returns tool result", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "weather-sse",
			"toolName":          "get_weather",
			"arguments":     map[string]any{"city": "Denver"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.mcp.weather-sse.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))

		assert.False(t, result.IsError, "expected isError=false")
		require.NotEmpty(t, result.Content)
		assert.Equal(t, "text", result.Content[0].Type)
		assert.True(t, strings.Contains(result.Content[0].Text, "Denver"),
			"expected tool result to mention Denver, got: %s", result.Content[0].Text)
	})
}
