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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hotReload))
}

// hotReload verifies that updating an MCPServer resource file triggers
// hot-reload: subsequent tool calls use the new server configuration.
type hotReload struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client

	resourceDir string
	mcpFilePath string
	serverAPort int
	serverBPort int
}

func (s *hotReload) Setup(t *testing.T) []framework.Option {
	// Server A returns "response-from-A".
	srvA := mcp.NewServer(&mcp.Implementation{Name: "server-a", Version: "v1"}, nil)
	mcp.AddTool(srvA, &mcp.Tool{Name: "echo", Description: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "response-from-A"}}}, struct{}{}, nil
	})
	procA := prochttp.New(t, prochttp.WithHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srvA }, nil)))
	s.serverAPort = procA.Port()

	// Server B returns "response-from-B".
	srvB := mcp.NewServer(&mcp.Implementation{Name: "server-b", Version: "v1"}, nil)
	mcp.AddTool(srvB, &mcp.Tool{Name: "echo", Description: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "response-from-B"}}}, struct{}{}, nil
	})
	procB := prochttp.New(t, prochttp.WithHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srvB }, nil)))
	s.serverBPort = procB.Port()

	appProc := app.New(t)
	s.sched = scheduler.New(t)
	s.place = placement.New(t)

	// Write the initial MCPServer resource pointing to server A.
	s.resourceDir = t.TempDir()
	s.mcpFilePath = filepath.Join(s.resourceDir, "mcpserver.yaml")
	s.writeMCPResource(t, s.serverAPort)

	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourcesDir(s.resourceDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, procA, procB, s.daprd),
	}
}

func (s *hotReload) writeMCPResource(t *testing.T, port int) {
	t.Helper()
	yaml := []byte(`apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: hotreload-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:` + strconv.Itoa(port) + "\n")
	require.NoError(t, os.WriteFile(s.mcpFilePath, yaml, 0o600))
}

func (s *hotReload) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	wfName := mcpnames.MCPCallToolWorkflowName("hotreload-server", "echo")
	input := map[string]any{"arguments": map[string]any{}}

	t.Run("initial call uses server A", func(t *testing.T) {
		status := runWorkflow(t, ctx, s.httpClient, s.daprd.HTTPPort(), wfName, input, 30*time.Second)
		require.Equal(t, statusCompleted, status.RuntimeStatus)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(status.Properties["dapr.workflow.output"]), &result))
		require.False(t, result.GetIsError(), "tool call failed: %v", result.GetContent())
		require.NotEmpty(t, result.GetContent())
		assert.Equal(t, "response-from-A", result.GetContent()[0].GetText().GetText())
	})

	t.Run("after hot-reload, call uses server B", func(t *testing.T) {
		s.writeMCPResource(t, s.serverBPort)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			status, err := tryRunWorkflow(ctx, s.httpClient, s.daprd.HTTPPort(), wfName, input, 5*time.Second)
			if !assert.NoError(c, err) {
				return
			}
			if !assert.Equal(c, statusCompleted, status.RuntimeStatus) {
				return
			}
			var result wfv1.CallMCPToolResponse
			if !assert.NoError(c, protojson.Unmarshal([]byte(status.Properties["dapr.workflow.output"]), &result)) {
				return
			}
			if !assert.False(c, result.GetIsError()) {
				return
			}
			if !assert.NotEmpty(c, result.GetContent()) {
				return
			}
			assert.Equal(c, "response-from-B", result.GetContent()[0].GetText().GetText())
		}, 30*time.Second, 500*time.Millisecond, "expected response from server B after hot-reload")
	})
}
