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
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workflowAccessPolicy))
}

// workflowAccessPolicy verifies that WorkflowAccessPolicy gates cross-app
// invocation of MCP workflows and cross-app middleware hook workflows.
//
// Three sidecars:
//   - caller:   triggers MCP tools cross-app
//   - target:   hosts MCPServers (denied-server, allowed-server, crossapp-hook-server)
//   - hook-app: hosts the cross-app middleware hook workflow
//
// Policies:
//   - target denies caller from invoking denied-server MCP workflows
//   - hook-app denies target from invoking remote_audit_workflow
type workflowAccessPolicy struct {
	sentryProc *sentry.Sentry
	place      *placement.Placement
	sched      *scheduler.Scheduler
	db         *sqlite.SQLite
	caller     *daprd.Daprd
	target     *daprd.Daprd
	hookApp    *daprd.Daprd
}

func (w *workflowAccessPolicy) Setup(t *testing.T) []framework.Option {
	w.sentryProc = sentry.New(t)
	w.place = placement.New(t, placement.WithSentry(t, w.sentryProc))
	w.sched = scheduler.New(t, scheduler.WithSentry(w.sentryProc), scheduler.WithID("dapr-scheduler-server-0"))
	w.db = sqlite.New(t, sqlite.WithActorStateStore(true), sqlite.WithCreateStateTables())

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "acl-test-server", Version: "v1"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "ping",
		Description: "Returns pong",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
		}, struct{}{}, nil
	})
	mcpSrvProc := prochttp.New(t, prochttp.WithHandler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil),
	))

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpaclconfig
spec:
  features:
  - name: WorkflowAccessPolicy
    enabled: true`), 0o600))

	// --- Target resources ---
	targetResDir := t.TempDir()

	// Policy on target: deny caller from denied-server MCP workflows and direct hook invocation.
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: target-acl
scopes:
- mcp-target
spec:
  defaultAction: allow
  rules:
  - callers:
    - appID: mcp-caller
    operations:
    - type: workflow
      name: "dapr.internal.mcp.denied-server.*"
      action: deny
    - type: workflow
      name: "local_hook_workflow"
      action: deny
`), 0o600))

	// MCPServer without middleware — deny test.
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "denied.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: denied-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
`, mcpSrvProc.Port()), 0o600))

	// MCPServer with local middleware hook — allow + local hook test.
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "allowed.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: allowed-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
  middleware:
    beforeCallTool:
      - workflow:
          workflowName: local_hook_workflow
`, mcpSrvProc.Port()), 0o600))

	// MCPServer with CROSS-APP middleware hook targeting hook-app.
	require.NoError(t, os.WriteFile(filepath.Join(targetResDir, "crossapp-hook.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: crossapp-hook-server
spec:
  endpoint:
    streamableHTTP:
      url: http://localhost:%d
  middleware:
    beforeCallTool:
      - workflow:
          workflowName: remote_audit_workflow
          appID: hook-app
`, mcpSrvProc.Port()), 0o600))

	// --- Hook-app resources ---
	hookAppResDir := t.TempDir()

	// Policy on hook-app: deny target from invoking remote_audit_workflow.
	// This tests that cross-app middleware hooks are subject to the remote app's access policy.
	require.NoError(t, os.WriteFile(filepath.Join(hookAppResDir, "policy.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: WorkflowAccessPolicy
metadata:
  name: hook-app-acl
scopes:
- hook-app
spec:
  defaultAction: allow
  rules:
  - callers:
    - appID: mcp-target
    operations:
    - type: workflow
      name: "remote_audit_workflow"
      action: deny
`), 0o600))

	w.caller = daprd.New(t,
		daprd.WithAppID("mcp-caller"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(w.db.GetComponent(t)),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithSentry(t, w.sentryProc),
	)
	w.target = daprd.New(t,
		daprd.WithAppID("mcp-target"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(targetResDir),
		daprd.WithResourceFiles(w.db.GetComponent(t)),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithSentry(t, w.sentryProc),
	)
	w.hookApp = daprd.New(t,
		daprd.WithAppID("hook-app"),
		daprd.WithNamespace("default"),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(hookAppResDir),
		daprd.WithResourceFiles(w.db.GetComponent(t)),
		daprd.WithPlacementAddresses(w.place.Address()),
		daprd.WithSchedulerAddresses(w.sched.Address()),
		daprd.WithSentry(t, w.sentryProc),
	)

	return []framework.Option{
		framework.WithProcesses(w.sentryProc, w.place, w.sched, w.db, mcpSrvProc, w.caller, w.target, w.hookApp),
	}
}

func (w *workflowAccessPolicy) Run(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	w.sched.WaitUntilRunning(t, ctx)
	w.caller.WaitUntilRunning(t, ctx)
	w.target.WaitUntilRunning(t, ctx)
	w.hookApp.WaitUntilRunning(t, ctx)

	callerReg := task.NewTaskRegistry()

	// Workflow that calls the DENIED MCP server cross-app.
	require.NoError(t, callerReg.AddWorkflowN("InvokeDeniedMCP", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow(
			mcpnames.MCPCallToolWorkflowName("denied-server", "ping"),
			task.WithChildWorkflowAppID("mcp-target"),
			task.WithChildWorkflowInput(map[string]any{
				"toolName":  "ping",
				"arguments": map[string]any{},
			}),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app MCP CallTool failed: %w", err)
		}
		return output, nil
	}))

	// Workflow that calls the ALLOWED MCP server cross-app (local middleware hook).
	require.NoError(t, callerReg.AddWorkflowN("InvokeAllowedMCP", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow(
			mcpnames.MCPCallToolWorkflowName("allowed-server", "ping"),
			task.WithChildWorkflowAppID("mcp-target"),
			task.WithChildWorkflowInput(map[string]any{
				"toolName":  "ping",
				"arguments": map[string]any{},
			}),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app MCP CallTool failed: %w", err)
		}
		return output, nil
	}))

	// Workflow that calls the MCP server whose middleware hook targets hook-app cross-app.
	require.NoError(t, callerReg.AddWorkflowN("InvokeCrossAppHookMCP", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow(
			mcpnames.MCPCallToolWorkflowName("crossapp-hook-server", "ping"),
			task.WithChildWorkflowAppID("mcp-target"),
			task.WithChildWorkflowInput(map[string]any{
				"toolName":  "ping",
				"arguments": map[string]any{},
			}),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app MCP CallTool failed: %w", err)
		}
		return output, nil
	}))

	// Workflow that tries to call the local hook workflow cross-app directly.
	require.NoError(t, callerReg.AddWorkflowN("InvokeHookDirect", func(ctx *task.WorkflowContext) (any, error) {
		var output string
		err := ctx.CallChildWorkflow(
			"local_hook_workflow",
			task.WithChildWorkflowAppID("mcp-target"),
		).Await(&output)
		if err != nil {
			return nil, fmt.Errorf("cross-app hook invocation failed: %w", err)
		}
		return output, nil
	}))

	// Target registers the local middleware hook workflow.
	targetReg := task.NewTaskRegistry()
	require.NoError(t, targetReg.AddWorkflowN("local_hook_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	// Hook-app registers the remote audit workflow.
	hookAppReg := task.NewTaskRegistry()
	require.NoError(t, hookAppReg.AddWorkflowN("remote_audit_workflow", func(ctx *task.WorkflowContext) (any, error) {
		return nil, nil
	}))

	callerClient := client.NewTaskHubGrpcClient(w.caller.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, callerClient.StartWorkItemListener(ctx, callerReg))
	targetClient := client.NewTaskHubGrpcClient(w.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, targetClient.StartWorkItemListener(ctx, targetReg))
	hookAppClient := client.NewTaskHubGrpcClient(w.hookApp.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, hookAppClient.StartWorkItemListener(ctx, hookAppReg))

	// Wait for actor types to be registered on all sidecars.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, len(w.caller.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(w.target.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
		assert.GreaterOrEqual(c, len(w.hookApp.GetMetadata(t, ctx).ActorRuntime.ActiveActors), 1)
	}, time.Second*20, time.Millisecond*10)

	t.Run("cross-app MCP CallTool denied by policy", func(t *testing.T) {
		id, err := callerClient.ScheduleNewWorkflow(ctx, "InvokeDeniedMCP")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"orchestration should fail because cross-app MCP call is denied by policy")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})

	t.Run("cross-app MCP CallTool allowed with local middleware hook", func(t *testing.T) {
		// The MCP CallTool is allowed by policy. The local middleware hook
		// (local_hook_workflow) runs under the target's own identity — the
		// caller-specific deny rule does not apply to local child workflows.
		id, err := callerClient.ScheduleNewWorkflow(ctx, "InvokeAllowedMCP")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		assert.Nil(t, metadata.GetFailureDetails(),
			"allowed MCP call with local middleware hook should succeed")
	})

	t.Run("cross-app middleware hook denied by remote app policy", func(t *testing.T) {
		// The MCP CallTool on crossapp-hook-server is allowed (caller → target).
		// But the beforeCallTool hook targets hook-app's remote_audit_workflow
		// cross-app (target → hook-app). The policy on hook-app denies target
		// from invoking remote_audit_workflow. The hook fails, which returns
		// isError=true (beforeCallTool error semantics).
		id, err := callerClient.ScheduleNewWorkflow(ctx, "InvokeCrossAppHookMCP")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		// beforeCallTool errors complete the workflow with isError=true so the
		// agent/LLM can reason about the failure and self-correct.
		assert.True(t, api.OrchestrationMetadataIsComplete(metadata),
			"workflow should complete (not fail) — the hook error is in the output")
	})

	t.Run("direct cross-app invocation of local hook workflow denied", func(t *testing.T) {
		// A caller trying to invoke the local hook workflow directly cross-app
		// is denied by the target's access policy.
		id, err := callerClient.ScheduleNewWorkflow(ctx, "InvokeHookDirect")
		require.NoError(t, err)

		metadata, err := callerClient.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		require.NotNil(t, metadata.GetFailureDetails(),
			"direct cross-app invocation of hook workflow should be denied by policy")
		assert.Contains(t, metadata.GetFailureDetails().GetErrorMessage(), "denied by workflow access policy")
	})
}
