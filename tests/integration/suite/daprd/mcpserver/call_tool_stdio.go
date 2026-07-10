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
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/workflow/httpapi"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callToolStdio))
}

// callToolStdio verifies that MCP tool calls work over the stdio transport.
// The mcpstdioserver helper binary is used as the stdio MCP server subprocess.
type callToolStdio struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *callToolStdio) Setup(t *testing.T) []framework.Option {
	binPath := binary.EnvValue("mcpstdioserver")
	require.NotEmpty(t, binPath, "mcpstdioserver helper binary path is not set")

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: stdio-server
spec:
  endpoint:
    stdio:
      command: `+binPath+`
`),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, s.daprd),
	}
}

func (s *callToolStdio) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("ListTools over stdio transport", func(t *testing.T) {
		status := httpapi.Run(t, ctx, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("stdio-server"), nil, 30*time.Second)
		require.Equal(t, httpapi.StatusCompleted, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result mcp.ListToolsResult
		require.NoError(t, json.Unmarshal([]byte(outputJSON), &result))
		require.Len(t, result.Tools, 1)
		assert.Equal(t, "stdio_echo", result.Tools[0].Name)
	})

	t.Run("CallTool over stdio transport", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{},
		}
		status := httpapi.Run(t, ctx, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("stdio-server", "stdio_echo"), input, 30*time.Second)
		require.Equal(t, httpapi.StatusCompleted, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result mcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.Contains(t, extractText(result.Content[0]), "stdio-pong")
	})
}
