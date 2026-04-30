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
	suite.Register(new(hotReloadAddDelete))
}

// hotReloadAddDelete verifies the full hot-reload lifecycle:
// 1. Start daprd with NO MCPServer resources.
// 2. Drop an MCPServer YAML → ListTools discovers tools.
// 3. Delete the YAML → ListTools workflow is no longer registered.
type hotReloadAddDelete struct {
	daprd       *daprd.Daprd
	place       *placement.Placement
	sched       *scheduler.Scheduler
	httpClient  *http.Client
	resourceDir string
	mcpPort     int
}

func (s *hotReloadAddDelete) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "dynamic-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "dynamic_tool",
		Description: "A dynamically added tool",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "dynamic-response"}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))
	s.mcpPort = mcpSrvProc.Port()

	s.sched = scheduler.New(t)
	s.place = placement.New(t)

	// Start with an empty resources dir — no MCPServer initially.
	s.resourceDir = t.TempDir()

	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourcesDir(s.resourceDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, mcpSrvProc, s.daprd),
	}
}

func (s *hotReloadAddDelete) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	mcpYAML := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: dynamic
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, s.mcpPort)

	mcpFilePath := filepath.Join(s.resourceDir, "mcpserver.yaml")

	t.Run("add MCPServer via hot-reload and verify ListTools works", func(t *testing.T) {
		// Drop the MCPServer YAML into the resources dir.
		require.NoError(t, os.WriteFile(mcpFilePath, []byte(mcpYAML), 0o600))

		// Poll until ListTools succeeds — hot-reload needs time to detect the file.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
				mcpnames.MCPListToolsWorkflowName("dynamic"), nil)

			status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 10*time.Second)
			if !assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus) {
				return
			}

			outputJSON := status.Properties["dapr.workflow.output"]
			if !assert.NotEmpty(c, outputJSON) {
				return
			}

			var result wfv1.ListMCPToolsResponse
			if !assert.NoError(c, protojson.Unmarshal([]byte(outputJSON), &result)) {
				return
			}
			assert.Len(c, result.GetTools(), 1)
			assert.Equal(c, "dynamic_tool", result.GetTools()[0].Name)
		}, 30*time.Second, time.Second)
	})

	t.Run("delete MCPServer via hot-reload and verify workflow unregistered", func(t *testing.T) {
		// Remove the MCPServer YAML.
		require.NoError(t, os.Remove(mcpFilePath))

		// Poll until the ListTools workflow fails with "not registered".
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
				mcpnames.MCPListToolsWorkflowName("dynamic"), nil)

			status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 10*time.Second)
			assert.Equal(c, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, status.RuntimeStatus,
				"ListTools should fail after MCPServer is deleted")
		}, 30*time.Second, time.Second)
	})
}
