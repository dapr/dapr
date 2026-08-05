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
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	dtclient "github.com/dapr/durabletask-go/client"

	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/workflow/httpapi"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(registrationRetry))
}

// registrationRetry covers an MCP server not serving yet when daprd loads its
// resource. Ensure it is retried if the server is not ready yet on first connection.
type registrationRetry struct {
	daprd    *daprd.Daprd
	place    *placement.Placement
	sched    *scheduler.Scheduler
	attempts *atomic.Int32
	// failFirst is how many requests are refused before the server serves.
	failFirst int32
}

func (r *registrationRetry) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "flaky", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Get current weather",
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "sunny"}}}, struct{}{}, nil
	})

	r.attempts = new(atomic.Int32)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)

	// What a proxy returns while its origin is still starting.
	r.failFirst = 2
	flaky := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.attempts.Add(1) <= r.failFirst {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, req)
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(flaky))
	appProc := app.New(t)

	r.sched = scheduler.New(t)
	r.place = placement.New(t)
	r.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(r.place.Address()),
		daprd.WithSchedulerAddresses(r.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: flaky
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpSrvProc.Port())),
	)

	return []framework.Option{
		framework.WithProcesses(r.place, r.sched, appProc, mcpSrvProc, r.daprd),
	}
}

func (r *registrationRetry) Run(t *testing.T, ctx context.Context) {
	r.sched.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)
	r.daprd.WaitUntilRunning(t, ctx)

	t.Run("daprd dials again after a refusal", func(t *testing.T) {
		assert.Greater(t, r.attempts.Load(), r.failFirst,
			"daprd stopped dialling while the server was still refusing")
	})

	t.Run("the server is discoverable", func(t *testing.T) {
		servers := r.daprd.GetMetaMCPServers(t, ctx)
		names := make([]string, 0, len(servers))
		for _, m := range servers {
			names = append(names, m.GetName())
		}
		assert.Contains(t, names, "flaky")
	})

	// A server can sit in metadata having never registered, so only running its
	// workflow proves it.
	t.Run("its tools are usable", func(t *testing.T) {
		httpClient := fclient.HTTP(t)
		taskhubClient := dtclient.NewTaskHubGrpcClient(r.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

		instanceID := httpapi.Start(t, ctx, httpClient, r.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("flaky"), map[string]any{})

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result mcp.ListToolsResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))

		names := make([]string, len(result.Tools))
		for i, tool := range result.Tools {
			names[i] = tool.Name
		}
		assert.ElementsMatch(t, []string{"get_weather"}, names)
	})
}
