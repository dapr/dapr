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

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"

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
	suite.Register(new(middlewareListToolsHooks))
}

// middlewareListToolsHooks verifies ListTools middleware hooks:
// 1. beforeListTools hook error blocks discovery.
// 2. afterListTools hook error fails the workflow.
type middlewareListToolsHooks struct {
	daprd      *daprd.Daprd
	place      *placement.Placement
	sched      *scheduler.Scheduler
	httpClient *http.Client
}

func (s *middlewareListToolsHooks) Setup(t *testing.T) []framework.Option {
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "test-listhook-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "some_tool",
		Description: "A tool for testing",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
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
  name: before-list-deny
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    beforeListTools:
      - workflow:
          workflowName: deny_list_workflow
---
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: after-list-fail
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%[1]d
  middleware:
    afterListTools:
      - workflow:
          workflowName: after_list_fail_workflow
`, mcpPort)),
	)

	return []framework.Option{
		framework.WithProcesses(s.place, s.sched, appProc, mcpSrvProc, s.daprd),
	}
}

func (s *middlewareListToolsHooks) Run(t *testing.T, ctx context.Context) {
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

	r.AddWorkflowN("deny_list_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("listing tools is not allowed")
	})

	r.AddWorkflowN("after_list_fail_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, errors.New("post-list audit failed")
	})

	workerCtx, cancelWorker := context.WithCancel(ctx)
	require.NoError(t, backendClient.StartWorkItemListener(workerCtx, r))
	defer cancelWorker()

	t.Run("beforeListTools hook error blocks discovery", func(t *testing.T) {
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("before-list-deny"), nil)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED.String(), status.RuntimeStatus,
			"beforeListTools error should fail the workflow")
	})

	t.Run("afterListTools hook error fails workflow", func(t *testing.T) {
		instanceID := startMCPWorkflow(ctx, t, s.httpClient, s.daprd.HTTPPort(),
			mcpnames.MCPListToolsWorkflowName("after-list-fail"), nil)

		status := pollWorkflowCompletion(ctx, t, s.httpClient, s.daprd.HTTPPort(), instanceID, 30*time.Second)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED.String(), status.RuntimeStatus,
			"afterListTools error should fail the workflow")
	})
}
