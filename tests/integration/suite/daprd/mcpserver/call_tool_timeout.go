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

	"github.com/dapr/durabletask-go/api"
	dtclient "github.com/dapr/durabletask-go/client"

	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/dapr/tests/integration/framework"
	fclient "github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/workflow/httpapi"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callToolTimeout))
}

// slowToolDelay is longer than the fixed 5s the MCPServer's HTTP client used to
// wait for response headers, whatever timeout the MCPServer set.
const slowToolDelay = 6 * time.Second

// callToolTimeout verifies that an MCPServer's endpoint.timeout is the
// per-call deadline for CallTool, in both directions. The upstream answers in
// plain JSON, so its response headers leave only with the tool's result: the
// whole call is time-to-first-byte, which is what the HTTP client's wait for
// response headers bounds.
type callToolTimeout struct {
	daprd *daprd.Daprd
	place *placement.Placement
	sched *scheduler.Scheduler
}

func (s *callToolTimeout) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-calltool-timeout-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "slow_quote",
		Description: "Answer after slowToolDelay",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		select {
		case <-time.After(slowToolDelay):
		case <-ctx.Done():
			return nil, struct{}{}, ctx.Err()
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "quoted"}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return mcpSrv },
			&mcp.StreamableHTTPOptions{JSONResponse: true},
		),
	))

	appProc := app.New(t)

	s.sched = scheduler.New(t)
	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithAppPort(appProc.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithSchedulerAddresses(s.sched.Address()),
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: patient
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
      timeout: 15s
`, mcpSrvProc.Port()),
			fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: impatient
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
      timeout: 1s
`, mcpSrvProc.Port()),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *callToolTimeout) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	httpClient := fclient.HTTP(t)
	taskhubClient := dtclient.NewTaskHubGrpcClient(s.daprd.GRPCConn(t, ctx), logger.New(t))

	callSlowQuote := func(t *testing.T, server string) (*mcp.CallToolResult, time.Duration) {
		t.Helper()
		start := time.Now()
		instanceID := httpapi.Start(t, ctx, httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName(server, "slow_quote"),
			map[string]any{"arguments": map[string]any{}})

		metadata, err := taskhubClient.WaitForWorkflowCompletion(
			ctx, api.InstanceID(instanceID), api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.True(t, api.WorkflowMetadataIsComplete(metadata))

		var result mcp.CallToolResult
		require.NoError(t, json.Unmarshal([]byte(metadata.GetOutput().GetValue()), &result))
		return &result, time.Since(start)
	}

	t.Run("a call slower than 5s completes within its MCPServer's timeout", func(t *testing.T) {
		result, elapsed := callSlowQuote(t, "patient")
		require.False(t, result.IsError, "the call was cut after %s with the MCPServer's timeout at 15s: %s",
			elapsed, resultText(result))
		require.NotEmpty(t, result.Content)
		assert.Equal(t, "quoted", extractText(result.Content[0]))
		assert.GreaterOrEqual(t, elapsed, slowToolDelay, "the tool answered before its delay, so this proved nothing")
	})

	t.Run("a call slower than its MCPServer's timeout is cut at that timeout", func(t *testing.T) {
		result, elapsed := callSlowQuote(t, "impatient")
		assert.True(t, result.IsError, "expected the 1s timeout to cut the call, got: %s", resultText(result))
		assert.Less(t, elapsed, slowToolDelay, "the call outlived the MCPServer's 1s timeout")
	})
}

// resultText is the text of a CallToolResult's first content item, or "" when
// it carries none.
func resultText(result *mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	return extractText(result.Content[0])
}
