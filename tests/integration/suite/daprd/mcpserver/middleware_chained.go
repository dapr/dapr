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
	suite.Register(new(middlewareChained))
}

// middlewareChained verifies that multiple middleware hooks execute in array
// order and that mutations flow through the chain:
//
// beforeCallTool chain:
//   - Hook A (noop, passes) → Hook B (returns error) → tool never called, isError returned
//
// afterCallTool chain:
//   - Hook A (noop) → Hook B (returns error) → workflow FAILS
type middlewareChained struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *middlewareChained) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-chained-server", Version: "v1"}, nil)

	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes the input",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echo-ok"}},
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
  name: chained-before
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    beforeCallTool:
      - workflow:
          workflowName: chain_hook_a
      - workflow:
          workflowName: chain_hook_b_fail
---
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: chained-after
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    afterCallTool:
      - workflow:
          workflowName: after_chain_a
      - workflow:
          workflowName: after_chain_b_fail
`, mcpPort)),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *middlewareChained) Run(t *testing.T, ctx context.Context) {
	s.sched.WaitUntilRunning(t, ctx)
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	s.httpClient = fclient.HTTP(t)

	conn, err := grpc.DialContext(ctx, //nolint:staticcheck
		s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	backendClient := client.NewTaskHubGrpcClient(conn, backend.DefaultLogger())
	r := task.NewTaskRegistry()

	// beforeCallTool chain hooks:
	// Hook A passes through (noop).
	r.AddWorkflowN("chain_hook_a", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	// Hook B fails — should short-circuit the chain.
	r.AddWorkflowN("chain_hook_b_fail", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("hook-b: access denied in chain")
	})

	// afterCallTool chain hooks:
	// Hook A passes through (noop).
	r.AddWorkflowN("after_chain_a", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	})
	// Hook B fails — should fail the entire workflow.
	r.AddWorkflowN("after_chain_b_fail", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("after-hook-b: audit failure in chain")
	})

	workerCtx, cancelWorker := context.WithCancel(ctx)
	require.NoError(t, backendClient.StartWorkItemListener(workerCtx, r))
	defer cancelWorker()

	t.Run("beforeCallTool chain: hook A passes, hook B fails → isError", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("chained-before", "echo"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED.String(), status.RuntimeStatus,
			"beforeCallTool error should produce isError result, not workflow failure")

		outputJSON := status.Properties["dapr.workflow.output"]
		require.NotEmpty(t, outputJSON)

		var result wfv1.CallMCPToolResponse
		require.NoError(t, protojson.Unmarshal([]byte(outputJSON), &result))
		assert.True(t, result.GetIsError(), "expected isError=true from chain short-circuit")
		require.NotEmpty(t, result.GetContent())
		assert.Contains(t, result.GetContent()[0].GetText().GetText(), "hook-b",
			"expected hook-b error in content, got: %s", result.GetContent()[0].GetText().GetText())
	})

	t.Run("afterCallTool chain: hook A passes, hook B fails → workflow FAILS", func(t *testing.T) {
		input := map[string]any{
			"arguments": map[string]any{},
		}
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPCallToolWorkflowName("chained-after", "echo"), input)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED.String(), status.RuntimeStatus,
			"afterCallTool chain error should fail the entire workflow")
	})
}
