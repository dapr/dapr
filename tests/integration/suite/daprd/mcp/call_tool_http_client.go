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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	daprmcp "github.com/dapr/dapr/pkg/runtime/mcp"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
)

func init() {
	suite.Register(new(callToolHTTPClient))
}

// callToolHTTPClient verifies that a plain HTTP client (no durabletask SDK) can invoke MCP tools via the Dapr workflow API.
// The routing through the built-in workflow orchestration is transparent to the HTTP caller.
type callToolHTTPClient struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *callToolHTTPClient) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-http-client-server", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Return current conditions for a city",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Weather in %s: sunny, 72°F", args.City)}},
		}, struct{}{}, nil
	})

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

func (s *callToolHTTPClient) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("plain HTTP client can invoke MCP tool via workflow API without SDK", func(t *testing.T) {
		// Start the CallTool workflow via a plain HTTP POST — no durabletask SDK needed.
		input := map[string]any{
			"mcpServerName": "weather",
			"toolName":          "get_weather",
			"arguments":     map[string]any{"city": "Portland"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.mcp.weather.CallTool", input)

		// Poll for completion using plain HTTP GET
		statusURL := fmt.Sprintf("http://localhost:%d/v1.0-beta1/workflows/dapr/%s",
			s.daprd.HTTPPort(), instanceID)

		type wfStatus struct {
			RuntimeStatus string            `json:"runtimeStatus"`
			Properties    map[string]string `json:"properties"`
		}

		var status wfStatus
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
			if !assert.NoError(c, err) {
				return
			}
			resp, err := s.httpClient.Do(req)
			if !assert.NoError(c, err) {
				return
			}
			defer resp.Body.Close()
			if !assert.NoError(c, json.NewDecoder(resp.Body).Decode(&status)) {
				return
			}
			assert.Equal(c, api.RUNTIME_STATUS_COMPLETED, status.RuntimeStatus)
		}, 30*time.Second, 100*time.Millisecond)

		// The tool output is stored in the dapr.workflow.output property.
		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON, "expected dapr.workflow.output to be populated")

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(outputJSON), &result))

		assert.False(t, result.IsError, "expected isError=false")
		require.NotEmpty(t, result.Content)
		assert.Equal(t, "text", result.Content[0].Type)
		assert.True(t, strings.Contains(result.Content[0].Text, "Portland"),
			"expected tool result to mention Portland, got: %s", result.Content[0].Text)
	})
}
