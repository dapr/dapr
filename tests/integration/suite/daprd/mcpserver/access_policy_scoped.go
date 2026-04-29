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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	suite.Register(new(accessPolicyScoped))
}

// accessPolicyScoped verifies that an MCPServer scoped to a different appID
// is not loaded by a sidecar with a non-matching appID. The workflows should
// not be registered, and the metadata API should not list the MCPServer.
type accessPolicyScoped struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *accessPolicyScoped) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-scoped-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "secret_tool",
		Description: "Should not be accessible",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "secret"}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	// The daprd runs as "my-agent" but the MCPServer is scoped to "other-app".
	s.daprd = daprd.New(t,
		daprd.WithAppID("my-agent"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: restricted-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
scopes:
  - other-app
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, mcpSrvProc, s.daprd),
	}
}

func (s *accessPolicyScoped) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("scoped MCPServer not in metadata", func(t *testing.T) {
		metadataURL := fmt.Sprintf("http://localhost:%d/v1.0/metadata", s.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
		require.NoError(t, err)

		resp, err := s.httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		var metadata struct {
			MCPServers []struct {
				Name string `json:"name"`
			} `json:"mcpServers"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&metadata))

		for _, srv := range metadata.MCPServers {
			assert.NotEqual(t, "restricted-mcp", srv.Name,
				"MCPServer scoped to other-app should not appear in my-agent's metadata")
		}
	})

	t.Run("scoped MCPServer workflow not registered", func(t *testing.T) {
		// Attempting to start the ListTools workflow should fail because the
		// MCPServer was filtered out and its workflows were never registered.
		input := map[string]any{}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("restricted-mcp"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, status.RuntimeStatus,
			"workflow for out-of-scope MCPServer should fail as not registered")
	})
}
