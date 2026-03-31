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
	"sync/atomic"
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
	suite.Register(new(headerInjection))
}

// headerInjection verifies that static headers declared in the MCPServer
// spec.headers are injected into all HTTP requests to the MCP server.
type headerInjection struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client

	capturedAPIKey atomic.Value // stores string
}

func (s *headerInjection) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-header-server", Version: "v1"}, nil)

	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes input",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
		}, struct{}{}, nil
	})

	// Wrap the MCP handler to capture the X-API-Key header from incoming requests.
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if key := r.Header.Get("X-API-Key"); key != "" {
			s.capturedAPIKey.Store(key)
		}
		return mcpSrv
	}, nil)

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(mcpHandler))

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
  name: authed-server
spec:
  endpoint:
    transport: streamable_http
    target:
      url: http://localhost:%d
  headers:
  - name: X-API-Key
    value: test-secret-key-12345
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *headerInjection) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

	t.Run("static header X-API-Key is injected into MCP requests", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "authed-server",
			"toolName":          "echo",
			"arguments":     map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.mcp.authed-server.CallTool", input)

		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].Text, "ok"))

		// Verify the MCP server actually received the injected header.
		capturedKey, ok := s.capturedAPIKey.Load().(string)
		require.True(t, ok, "expected X-API-Key header to have been captured")
		assert.Equal(t, "test-secret-key-12345", capturedKey)
	})
}
