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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/durabletask-go/api"

	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(callToolStdio))
}

// callToolStdio verifies that MCP tool calls work over the stdio transport.
// A small Go program is compiled and used as the stdio MCP server subprocess.
type callToolStdio struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

// stdioServerSource is a minimal Go MCP server that communicates over stdin/stdout.
const stdioServerSource = `package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	srv := mcp.NewServer(&mcp.Implementation{Name: "stdio-test", Version: "v1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stdio_echo",
		Description: "Echoes a message via stdio",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "stdio-pong"}},
		}, struct{}{}, nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := srv.Run(ctx, mcp.NewStdioTransport()); err != nil && ctx.Err() == nil {
		os.Exit(1)
	}
}
`

func (s *callToolStdio) Setup(t *testing.T) []framework.Option {
	// Write and compile a tiny stdio MCP server binary.
	tmpDir := t.TempDir()
	serverDir := filepath.Join(tmpDir, "stdiosrv")
	require.NoError(t, os.MkdirAll(serverDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "main.go"), []byte(stdioServerSource), 0o600))

	// Initialize a go module so `go build` works.
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "go.mod"), []byte(`module stdiosrv

go 1.24

require github.com/modelcontextprotocol/go-sdk v1.5.0
`), 0o600))

	// Build the server binary. This requires the MCP SDK to be available
	// in the module cache (it is, since the main dapr module depends on it).
	binPath := filepath.Join(tmpDir, "stdiosrv-bin")
	t.Logf("Building stdio MCP server binary at %s", binPath)

	// Use `go build` to compile the stdio server.
	// This test is skipped if the build fails (e.g. in CI without module cache).
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = serverDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("skipping stdio test: failed to build stdio server: %s\n%s", err, out)
	}

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
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("stdio-server"), nil)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.ListMCPToolsResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		require.Len(t, result.Tools, 1)
		assert.Equal(t, "stdio_echo", result.Tools[0].Name)
	})

	t.Run("CallTool over stdio transport", func(t *testing.T) {
		input := map[string]any{
			"toolName":  "stdio_echo",
			"arguments": map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("stdio-server"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].GetText().GetText(), "stdio-pong"))
	})
}
