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
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restartMidCall))
}

// restartMidCall verifies that an MCP tool-call activity retries automatically
// when daprd restarts while the call is in-flight. The workflow is durable:
// actor reminders in the scheduler re-deliver the pending activity to the new
// daprd instance after restart, and the SQLite state store provides persistent
// workflow history so the orchestration can resume correctly.
type restartMidCall struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	db         *sqlite.SQLite
	httpClient *http.Client

	callCount  atomic.Int32
	firstCall  chan struct{} // closed when first call begins
}

func (s *restartMidCall) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: SQLite used for persistent workflow state is not reliable on Windows CI")
	}

	s.firstCall = make(chan struct{})

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-restart-server", Version: "v1"}, nil)

	type cityInput struct {
		City string `json:"city"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "get_weather",
		Description: "Return current conditions for a city",
	}, func(reqCtx context.Context, _ *mcp.CallToolRequest, args cityInput) (*mcp.CallToolResult, struct{}, error) {
		n := s.callCount.Add(1)
		if n == 1 {
			// Signal that the first call has started, then block until the
			// HTTP connection is dropped (i.e. daprd is killed). This ensures
			// the activity is in-flight when daprd is restarted.
			close(s.firstCall)
			<-reqCtx.Done()
			// Return an error so the MCP SDK cleans up cleanly.
			return nil, struct{}{}, reqCtx.Err()
		}
		// Subsequent calls (after restart) complete normally.
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("sunny in %s", args.City)}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	appProc := app.New(t)

	// SQLite provides persistent actor state store so the workflow history
	// survives the daprd restart.
	s.db = sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithResourceFiles(s.db.GetComponent(t)),
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

func (s *restartMidCall) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	t.Run("activity retries after daprd restart mid tool call", func(t *testing.T) {
		input := map[string]any{
			"mcpServerName": "weather",
			"toolName":          "get_weather",
			"arguments":     map[string]any{"city": "Seattle"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			"dapr.mcp.weather.CallTool", input)

		// Wait until the first MCP tool call is in-flight inside the activity.
		select {
		case <-s.firstCall:
		case <-ctx.Done():
			require.Fail(t, "timed out waiting for first tool call to start")
		}

		// Restart daprd. The in-flight activity is interrupted (its HTTP
		// connection to the MCP server is dropped, which cancels reqCtx).
		// The scheduler retains actor reminders for the pending activity so the
		// new daprd instance will re-deliver and re-execute it.
		s.daprd.Restart(t, ctx)
		s.daprd.WaitUntilRunning(t, ctx)

		// Reconnect the task-hub client to the restarted daprd instance.
		taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), backend.DefaultLogger())

		// Wait for the orchestration to complete. The scheduler's actor
		// reminder re-delivers the pending activity to the new daprd, which
		// calls the MCP server a second time and succeeds.
		completionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		t.Cleanup(cancel)
		metadata, err := taskhubClient.WaitForOrchestrationCompletion(
			completionCtx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
		assert.Nil(t, metadata.GetFailureDetails(),
			"expected orchestration to succeed after daprd restart")

		var result daprmcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		assert.False(t, result.IsError)
		require.NotEmpty(t, result.Content)
		assert.True(t, strings.Contains(result.Content[0].Text, "Seattle"),
			"expected tool result to mention Seattle, got: %s", result.Content[0].Text)

		// The tool was called at least twice: once before the restart (which
		// was abandoned when daprd died) and at least once after (the retry).
		assert.GreaterOrEqual(t, int(s.callCount.Load()), 2,
			"expected tool to be called at least twice (original + retry after restart)")
	})
}
