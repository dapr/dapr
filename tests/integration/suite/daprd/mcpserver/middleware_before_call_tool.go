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
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

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
	suite.Register(new(middlewareBeforeCallTool))
}

// middlewareBeforeCallTool verifies beforeCallTool middleware hooks:
// 1. A gating hook that returns an error blocks the tool call with isError=true.
// 2. A pass-through hook (no error, no mutation) allows the tool call to proceed normally.
type middlewareBeforeCallTool struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *middlewareBeforeCallTool) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-middleware-server", Version: "v1"}, nil)

	type msgInput struct {
		Msg string `json:"msg"`
	}
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes the msg argument",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args msgInput) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo: " + args.Msg}},
		}, struct{}{}, nil
	})

	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	appProc := app.New(t)
	mcpPort := mcpSrvProc.Port()

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
  name: gated
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    beforeCallTool:
      - workflow:
          workflowName: gate_deny_workflow
---
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: passthrough
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    beforeCallTool:
      - workflow:
          workflowName: noop_workflow
`, mcpPort)),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *middlewareBeforeCallTool) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	// Connect a gRPC worker that registers the middleware workflows.
	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())

	r := task.NewTaskRegistry()

	// gate_deny_workflow: always returns an error → blocks the tool call.
	r.AddWorkflowN("gate_deny_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("access denied by policy")
	})

	// noop_workflow: succeeds immediately → tool call proceeds.
	r.AddWorkflowN("noop_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})

	workerCtx, cancelWorker := context.WithCancel(ctx)
	require.NoError(t, backendClient.StartWorkItemListener(workerCtx, r))
	defer cancelWorker()

	// beforeCallTool errors complete the workflow with isError=true (not a workflow
	// failure) so the calling agent/LLM receives a structured error it can reason
	// about — retry, choose a different tool, or inform the user.
	t.Run("gate hook blocks tool call with isError", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{"msg": "hello"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("gated", "echo"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus,
			"CallTool with gate hook should complete (not fail) — the error is in the output")

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.True(t, result.GetIsError(), "expected isError=true when gate hook denies")
		require.NotEmpty(t, result.GetContent())
		assert.Contains(t, result.GetContent()[0].GetText().GetText(), "access denied by policy",
			"expected gate error in content, got: %s", result.GetContent()[0].GetText().GetText())
	})

	t.Run("pass-through hook allows tool call", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{"msg": "hello"},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("passthrough", "echo"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, status.RuntimeStatus)

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.False(t, result.GetIsError(), "expected success when noop hook passes through")
	})
}
