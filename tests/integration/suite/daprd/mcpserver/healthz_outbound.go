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
	"time"

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
	suite.Register(new(healthzOutboundGate))
}

// healthzOutboundGate verifies /v1.0/healthz/outbound stays 503 until flushOutstandingMCPServers drains.
type healthzOutboundGate struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler
}

func (s *healthzOutboundGate) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "slow-server", Version: "v1"}, nil)
	type empty struct{}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "noop",
		Description: "Returns nothing.",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, empty, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: ""}},
		}, empty{}, nil
	})

	// One-shot ~2s delay on the first request — opens an observable window
	// while daprd's HTTP server is up but MCPServer load is still in flight.
	base := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	var delayed atomic.Bool
	slowHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if delayed.CompareAndSwap(false, true) {
			time.Sleep(2 * time.Second)
		}
		base.ServeHTTP(w, r)
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(slowHandler))
	appProc := app.New(t)

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: slow
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

func (s *healthzOutboundGate) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)

	httpClient := fclient.HTTP(t)
	outboundURL := fmt.Sprintf("http://%s/v1.0/healthz/outbound", s.daprd.HTTPAddress())

	// Expect 503 throughout the slow-upstream window, then 204 after flush drains.
	t.Run("/healthz/outbound stays NotReady until MCPServer load completes", func(t *testing.T) {
		var sawNotReady bool
		var sawReady bool
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, outboundURL, nil)
			require.NoError(t, err)
			resp, err := httpClient.Do(req)
			if err != nil {
				// HTTP server not up yet — retry.
				time.Sleep(50 * time.Millisecond)
				continue
			}
			switch resp.StatusCode {
			case http.StatusServiceUnavailable:
				sawNotReady = true
			case http.StatusNoContent:
				sawReady = true
			}
			resp.Body.Close()
			if sawReady {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		assert.True(t, sawNotReady,
			"expected 503 during MCPServer load (regression: Ready() ran before loadMCPServers)")
		assert.True(t, sawReady, "expected 204 after MCPServer load completes")
	})

	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("ListTools workflow is schedulable once /healthz/outbound is Ready", func(t *testing.T) {
		taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
		instanceID := httpapi.Start(t, ctx, httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("slow"), map[string]any{})

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result mcp.ListToolsResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		names := make([]string, len(result.Tools))
		for i, tool := range result.Tools {
			names[i] = tool.Name
		}
		assert.ElementsMatch(t, []string{"noop"}, names)
	})
}
