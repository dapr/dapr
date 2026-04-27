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

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/logger"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcptypes "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/types"
	"github.com/dapr/dapr/pkg/security"
)

var workerLog = logger.NewLogger("dapr.runtime.wfengine.inprocess.mcp.worker")

// Options configures the MCP in-process workflow subsystem.
type Options struct {
	// Store is required; it is used to look up MCPServer manifests and fetch secrets at call time.
	Store *compstore.ComponentStore
	// Security enables SPIFFE workload identity JWT injection. If nil, SPIFFE is skipped.
	Security security.Handler
}

const (
	// workflowVersion is the version name used when registering versioned workflows.
	workflowVersion = "v1"

	// defaultMCPTimeout is the per-call deadline when no endpoint.timeout is set.
	defaultMCPTimeout = 30 * time.Second

	mcpClientName    = "dapr"
	mcpClientVersion = "v1alpha1"

	jsonFieldRequired = "required"
)

// holders tracks active session holders per server for hot-reload cleanup.
var holders sync.Map // map[string]*sessionHolder

// RegisterMCPServer registers workflows and activities for a single MCPServer.
// Builds the HTTP client and MCP session eagerly — similar to component Init().
// Safe to call on hot-reload — registry upserts replace existing entries.
func RegisterMCPServer(registry *task.TaskRegistry, server mcpserverapi.MCPServer, opts Options) error {
	// Remove the previous holder but don't close it — in-flight activities
	// may still reference it. The old holder becomes unreachable once no
	// closures reference it and GC cleans up the session.
	holders.Delete(server.Name)

	holder, err := newSessionHolder(&server, opts.Store, opts.Security)
	if err != nil {
		return fmt.Errorf("MCPServer %q: failed to connect: %w", server.Name, err)
	}
	holders.Store(server.Name, holder)

	schemas := &toolSchemaCache{}
	orchestrator := makeOrchestrator(server, opts.Store)
	listActivity := makeListToolsActivity(server, holder, schemas)
	callActivity := makeCallToolActivity(server, holder, schemas, opts)

	listWF := mcptypes.ListToolsWorkflowName(server.Name)
	if err := registry.AddVersionedWorkflowN(listWF, workflowVersion, true, orchestrator); err != nil {
		return fmt.Errorf("failed to register workflow %q: %w", listWF, err)
	}
	callWF := mcptypes.CallToolWorkflowName(server.Name)
	if err := registry.AddVersionedWorkflowN(callWF, workflowVersion, true, orchestrator); err != nil {
		return fmt.Errorf("failed to register workflow %q: %w", callWF, err)
	}
	listAct := mcptypes.ListToolsActivityName(server.Name)
	if err := registry.AddActivityN(listAct, listActivity); err != nil {
		return fmt.Errorf("failed to register activity %q: %w", listAct, err)
	}
	callAct := mcptypes.CallToolActivityName(server.Name)
	if err := registry.AddActivityN(callAct, callActivity); err != nil {
		return fmt.Errorf("failed to register activity %q: %w", callAct, err)
	}
	return nil
}

// UnregisterMCPServer removes workflows and activities for a deleted MCPServer.
// In-flight workflows that already captured closures continue to completion;
// only new workflow starts will fail with "not found".
func UnregisterMCPServer(registry *task.TaskRegistry, serverName string) {
	holders.Delete(serverName)

	registry.RemoveVersionedWorkflow(mcptypes.ListToolsWorkflowName(serverName))
	registry.RemoveVersionedWorkflow(mcptypes.CallToolWorkflowName(serverName))
	registry.RemoveActivity(mcptypes.ListToolsActivityName(serverName))
	registry.RemoveActivity(mcptypes.CallToolActivityName(serverName))
}

// makeOrchestrator returns the wildcard orchestrator function, closing over the
// component store for middleware lookup.
//
// For each suffix:
//
// ListTools path:
//   - beforeListTools is awaited; any error fails the workflow.
//   - dapr.internal.mcp.list-tools activity errors fail the workflow.
//   - afterListTools hooks are awaited; errors are logged but do not affect the result.
//
// CallTool path:
//   - beforeCallTool is awaited; any error aborts with CallMCPToolResponse{IsError:true}.
//   - dapr.internal.mcp.call-tool activity errors are returned as CallMCPToolResponse{IsError:true}.
//   - afterCallTool hooks are awaited; errors are logged but do not affect the result.
func makeOrchestrator(server mcpserverapi.MCPServer, store *compstore.ComponentStore) func(*task.WorkflowContext) (any, error) {
	serverName := server.Name
	return func(ctx *task.WorkflowContext) (any, error) {
		name := ctx.Name

		switch {
		case strings.HasSuffix(name, mcptypes.MethodListTools):
			if err := runBeforeListTools(ctx, &server, serverName); err != nil {
				return nil, errors.New("beforeListTools failed: " + err.Error())
			}

			var result wfv1.ListMCPToolsResponse
			t := ctx.CallActivity(mcptypes.ListToolsActivityName(serverName), task.WithActivityInput(nil))
			if err := t.Await(&result); err != nil {
				return nil, errors.New("list-tools activity failed: " + err.Error())
			}

			final, err := runAfterListTools(ctx, &server, serverName, &result)
			if err != nil {
				return nil, fmt.Errorf("afterListTools failed: %w", err)
			}
			return final, nil

		case strings.HasSuffix(name, mcptypes.MethodCallTool):
			var input wfv1.MCPCallToolWorkflowInput
			if err := ctx.GetInput(&input); err != nil {
				return errorResult("failed to parse CallToolInput: %s", err), nil
			}
			if input.ToolName == "" {
				return nil, fmt.Errorf("CallTool requires a non-empty tool_name")
			}

			// beforeCallTool middleware pipeline — may mutate arguments.
			arguments, err := runBeforeCallTool(ctx, &server, serverName, input.ToolName, input.Arguments)
			if err != nil {
				return errorResult("beforeCallTool: %s", err), nil
			}

			// Convert structpb.Struct → map[string]any for the activity (MCP SDK needs a map).
			var argMap map[string]any
			if arguments != nil {
				argMap = arguments.AsMap()
			}

			actInput := activityCallToolInput{
				ToolName:  input.ToolName,
				Arguments: argMap,
			}
			var result wfv1.CallMCPToolResponse
			t := ctx.CallActivity(mcptypes.CallToolActivityName(serverName), task.WithActivityInput(actInput))
			if err := t.Await(&result); err != nil {
				errResult := errorResult("%s", err)
				final, hookErr := runAfterCallTool(ctx, &server, serverName, input.ToolName, arguments, errResult)
				if hookErr != nil {
					return nil, fmt.Errorf("afterCallTool failed: %w", hookErr)
				}
				return final, nil
			}

			final, hookErr := runAfterCallTool(ctx, &server, serverName, input.ToolName, arguments, &result)
			if hookErr != nil {
				return nil, fmt.Errorf("afterCallTool failed: %w", hookErr)
			}
			return final, nil

		default:
			return nil, fmt.Errorf("unknown MCP workflow name %q: expected suffix %q or %q",
				name, mcptypes.MethodListTools, mcptypes.MethodCallTool)
		}
	}
}

// errorResult returns a CallMCPToolResponse with is_error=true and a single text content block.
func errorResult(format string, args ...any) *wfv1.CallMCPToolResponse {
	return &wfv1.CallMCPToolResponse{
		IsError: true,
		Content: []*wfv1.MCPContentBlock{{
			Content: &wfv1.MCPContentBlock_Text{
				Text: &wfv1.MCPTextContent{Text: fmt.Sprintf(format, args...)},
			},
		}},
	}
}

// buildTransport constructs the appropriate mcp.Transport for the given MCPServer.
// The httpClient is used for HTTP-based transports (streamableHTTP, sse).
func buildTransport(server *mcpserverapi.MCPServer, httpClient *http.Client) (mcp.Transport, error) {
	switch {
	case server.Spec.Endpoint.StreamableHTTP != nil:
		return &mcp.StreamableClientTransport{
			Endpoint:   server.Spec.Endpoint.StreamableHTTP.URL,
			HTTPClient: httpClient,
		}, nil

	case server.Spec.Endpoint.SSE != nil:
		return &mcp.SSEClientTransport{
			Endpoint:   server.Spec.Endpoint.SSE.URL,
			HTTPClient: httpClient,
		}, nil

	case server.Spec.Endpoint.Stdio != nil:
		cmd := exec.Command(server.Spec.Endpoint.Stdio.Command, server.Spec.Endpoint.Stdio.Args...) //nolint:gosec
		// Inherit the parent environment (PATH, HOME, etc.) so the subprocess can
		// locate its interpreter and dependencies; configured vars are appended
		// and take precedence over any inherited entries with the same name.
		if len(server.Spec.Endpoint.Stdio.Env) > 0 {
			cmd.Env = os.Environ()
			for _, env := range server.Spec.Endpoint.Stdio.Env {
				cmd.Env = append(cmd.Env, env.Name+"="+env.Value.String())
			}
		}
		return &mcp.CommandTransport{Command: cmd}, nil

	default:
		return nil, fmt.Errorf("no transport configured for MCPServer %q: set one of streamableHTTP, sse, or stdio", server.Name)
	}
}


// callTimeout returns the per-call deadline for the given MCPServer.
// Falls back to defaultMCPTimeout when no timeout is configured.
func callTimeout(server *mcpserverapi.MCPServer) time.Duration {
	switch {
	case server.Spec.Endpoint.StreamableHTTP != nil && server.Spec.Endpoint.StreamableHTTP.Timeout != nil:
		return server.Spec.Endpoint.StreamableHTTP.Timeout.Duration
	case server.Spec.Endpoint.SSE != nil && server.Spec.Endpoint.SSE.Timeout != nil:
		return server.Spec.Endpoint.SSE.Timeout.Duration
	default:
		return defaultMCPTimeout
	}
}

// withDeadline creates a context with the given timeout, or returns the
// original context unchanged when timeout is non-positive.
func withDeadline(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// structFromMap converts a map[string]any to a *structpb.Struct.
// Returns nil if the input map is nil.
func structFromMap(m map[string]any) *structpb.Struct {
	if m == nil {
		return nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		workerLog.Warnf("failed to convert map to structpb.Struct: %s", err)
		return nil
	}
	return s
}

// stringDeref returns the dereferenced string or "" if nil.
func stringDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
