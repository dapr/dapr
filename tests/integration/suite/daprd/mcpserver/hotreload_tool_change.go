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
	"os"
	"path/filepath"
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
)

func init() {
	suite.Register(new(hotReloadToolChange))
}

// hotReloadToolChange verifies that when an MCPServer is hot-reloaded and the
// upstream MCP server's tool list has changed, the per-tool CallTool workflows
// are updated: old tools are unregistered and new tools are registered.
//
// Setup:
// - Start with MCP server A exposing tool "alpha".
// - Hot-reload to MCP server B exposing tool "beta" (alpha removed).
// - Verify: alpha workflow fails (unregistered), beta workflow succeeds.
type hotReloadToolChange struct {
	daprd       *daprd.Daprd
	place       *placement.Placement
	sched       *scheduler.Scheduler
	httpClient  *http.Client
	resourceDir string
	serverAPort int
	serverBPort int
}

func (s *hotReloadToolChange) Setup(t *testing.T) []framework.Option {
	// Server A has tool "alpha".
	srvA := mcp.NewServer(&mcp.Implementation{Name: "server-a", Version: "v1"}, nil)
	mcp.AddTool(srvA, &mcp.Tool{Name: "alpha", Description: "tool alpha"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "alpha-result"}}}, struct{}{}, nil
	})
	procA := prochttp.New(t, prochttp.WithHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srvA }, nil)))
	s.serverAPort = procA.Port()

	// Server B has tool "beta" (alpha removed).
	srvB := mcp.NewServer(&mcp.Implementation{Name: "server-b", Version: "v1"}, nil)
	mcp.AddTool(srvB, &mcp.Tool{Name: "beta", Description: "tool beta"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "beta-result"}}}, struct{}{}, nil
	})
	procB := prochttp.New(t, prochttp.WithHandler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srvB }, nil)))
	s.serverBPort = procB.Port()

	s.sched = scheduler.New(t)
	s.place = placement.New(t)

	// Write initial MCPServer pointing to server A.
	s.resourceDir = t.TempDir()
	s.writeMCPResource(t, s.serverAPort)

	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourcesDir(s.resourceDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, procA, procB, s.daprd),
	}
}

func (s *hotReloadToolChange) writeMCPResource(t *testing.T, port int) {
	t.Helper()
	yaml := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: dynamic
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, port)
	require.NoError(t, os.WriteFile(filepath.Join(s.resourceDir, "mcpserver.yaml"), []byte(yaml), 0o600))
}

func (s *hotReloadToolChange) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("initial: alpha tool workflow works", func(t *testing.T) {
		input := map[string]any{"arguments": map[string]any{}}
		status, err := tryRunWorkflow(ctx, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("dynamic", "alpha"), input, 30*time.Second)
		require.NoError(t, err)
		require.Equal(t, statusCompleted, status.RuntimeStatus)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(status.Properties["dapr.workflow.output"]), &result))
		assert.False(t, result.GetIsError())
	})

	// Hot-reload: switch to server B.
	s.writeMCPResource(t, s.serverBPort)

	t.Run("after hot-reload: beta tool workflow works", func(t *testing.T) {
		input := map[string]any{"arguments": map[string]any{}}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			status, err := tryRunWorkflow(ctx, s.httpClient, s.daprd.HTTPPort(),
				mcpnames.MCPCallToolWorkflowName("dynamic", "beta"), input, 5*time.Second)
			if !assert.NoError(c, err) {
				return
			}
			assert.Equal(c, statusCompleted, status.RuntimeStatus)
		}, 30*time.Second, time.Second)
	})

	t.Run("after hot-reload: alpha tool workflow unregistered", func(t *testing.T) {
		// alpha is no longer registered: dapr's start endpoint hangs on unregistered
		// workflows (WaitForInstanceStart blocks). runWorkflow's short per-tick
		// timeout makes that hang surface as an error each tick — we assert the
		// error eventually appears.
		input := map[string]any{"arguments": map[string]any{}}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			_, err := tryRunWorkflow(ctx, s.httpClient, s.daprd.HTTPPort(),
				mcpnames.MCPCallToolWorkflowName("dynamic", "alpha"), input, 3*time.Second)
			assert.Error(c, err, "alpha tool start should hang or fail after hot-reload to server B")
		}, 30*time.Second, time.Second)
	})
}
