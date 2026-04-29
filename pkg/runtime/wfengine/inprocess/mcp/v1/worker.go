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
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/logger"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpnames "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/names"
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

// RegisterOptions bundles the inputs to RegisterMCPServer.
type RegisterOptions struct {
	// Ctx is used for the eager ListTools discovery call. Required.
	Ctx context.Context
	// Registry is the task registry to install workflows and activities into. Required.
	Registry *task.TaskRegistry
	// Holder is the connected MCP session for this server. Required; the caller
	// owns its lifecycle (creation, caching, cleanup).
	Holder *SessionHolder
	// Server is the MCPServer manifest being registered. Required.
	Server mcpserverapi.MCPServer
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

// RegisterMCPServer eagerly discovers tools on the MCP server, then registers
// per-tool CallTool workflows, the ListTools workflow, and supporting activities.
// Registers one CallTool.<toolName> workflow per tool for fine-grained observability.
// The caller is responsible for holder lifecycle (creation, caching, cleanup).
// Returns the discovered tool names (for unregistration tracking) or an error
// if tool discovery fails.
func RegisterMCPServer(opts RegisterOptions) ([]string, error) {
	tools, err := DiscoverTools(opts.Ctx, opts.Holder)
	if err != nil {
		return nil, fmt.Errorf("MCPServer %q: failed to discover tools: %w", opts.Server.Name, err)
	}

	schemas := &toolSchemaCache{}
	listCache := &toolListCache{}
	// Pre-populate the list cache with the eagerly-discovered tools so ListTools
	// workflow invocations return immediately without a redundant upstream call.
	// The cache is invalidated on hot-reload (this function runs again with fresh tools).
	listCache.store(tools)

	activityOpts := Options{Store: opts.Store, Security: opts.Security}
	orchestrator := makeOrchestrator(opts.Server, opts.Store)
	listActivity := makeListToolsActivity(opts.Server, opts.Holder, schemas, listCache)
	callActivity := makeCallToolActivity(opts.Server, opts.Holder, schemas, activityOpts)

	// Register per-tool CallTool workflows. Each tool gets its own workflow name
	// (dapr.internal.mcp.<server>.CallTool.<tool>) but they all share the same
	// orchestrator closure.
	toolNames := make([]string, len(tools))
	for i, tool := range tools {
		toolNames[i] = tool.Name
		wfName := mcpnames.MCPCallToolWorkflowName(opts.Server.Name, tool.Name)
		opts.Registry.UpsertVersionedWorkflowN(wfName, workflowVersion, true, orchestrator)
		// Pre-populate the schema cache from the eager ListTools results.
		if tool.InputSchema != nil {
			if raw, err := json.Marshal(tool.InputSchema.AsMap()); err == nil {
				schemas.set(tool.Name, raw) //nolint:errcheck
			}
		}
	}

	listWF := mcpnames.MCPListToolsWorkflowName(opts.Server.Name)
	opts.Registry.UpsertVersionedWorkflowN(listWF, workflowVersion, true, orchestrator)
	listAct := mcpnames.MCPListToolsActivityName(opts.Server.Name)
	opts.Registry.UpsertActivityN(listAct, listActivity)
	callAct := mcpnames.MCPCallToolActivityName(opts.Server.Name)
	opts.Registry.UpsertActivityN(callAct, callActivity)

	return toolNames, nil
}

// UnregisterMCPServer removes the ListTools workflow and activities for a deleted MCPServer.
// Per-tool CallTool workflows are removed by the caller (Executor) which tracks tool names.
func UnregisterMCPServer(registry *task.TaskRegistry, serverName string) {
	registry.RemoveVersionedWorkflow(mcpnames.MCPListToolsWorkflowName(serverName))
	registry.RemoveActivity(mcpnames.MCPListToolsActivityName(serverName))
	registry.RemoveActivity(mcpnames.MCPCallToolActivityName(serverName))
}

// maxListToolsPages bounds the number of pages we'll fetch from an MCP server's
// ListTools response. Guards against a misbehaving or malicious server returning
// infinite cursors.
const maxListToolsPages = 500

// DiscoverTools calls ListTools on the MCP server eagerly and returns the tool definitions.
// Called during RegisterMCPServer to discover tool names for per-tool workflow registration.
// Returns an error if the server returns more than maxListToolsPages — refusing to
// silently truncate the tool list (callers would later get confusing "workflow not
// registered" errors for the truncated tools).
func DiscoverTools(ctx context.Context, holder *SessionHolder) ([]*wfv1.MCPToolDefinition, error) {
	session, err := holder.Session(ctx)
	if err != nil {
		return nil, err
	}

	var tools []*wfv1.MCPToolDefinition
	var cursor string
	for page := 0; page < maxListToolsPages; page++ {
		params := &mcp.ListToolsParams{}
		if cursor != "" {
			params.Cursor = cursor
		}
		result, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("ListTools failed: %w", err)
		}
		for _, t := range result.Tools {
			td := &wfv1.MCPToolDefinition{Name: t.Name}
			if t.Description != "" {
				td.Description = &t.Description
			}
			if t.InputSchema != nil {
				if schema, ok := t.InputSchema.(map[string]any); ok {
					if s, err := structpb.NewStruct(schema); err == nil {
						td.InputSchema = s
					}
				}
			}
			tools = append(tools, td)
		}
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
	return nil, fmt.Errorf("MCP server returned more than %d pages of tools — refusing to truncate", maxListToolsPages)
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
		case strings.HasSuffix(name, mcpnames.MCPMethodListTools):
			if err := runBeforeListTools(ctx, &server, serverName); err != nil {
				return nil, fmt.Errorf("beforeListTools failed: %w", err)
			}

			var result wfv1.ListMCPToolsResponse
			t := ctx.CallActivity(mcpnames.MCPListToolsActivityName(serverName), task.WithActivityInput(nil))
			if err := t.Await(&result); err != nil {
				return nil, fmt.Errorf("list-tools activity failed: %w", err)
			}

			final, err := runAfterListTools(ctx, &server, serverName, &result)
			if err != nil {
				return nil, fmt.Errorf("afterListTools failed: %w", err)
			}
			return final, nil

		case strings.Contains(name, mcpnames.MCPMethodCallTool+"."):
			// Extract tool name from workflow name:
			// "dapr.internal.mcp.<server>.CallTool.<tool>" -> "<tool>"
			toolName := name[strings.LastIndex(name, ".")+1:]

			var input wfv1.MCPCallToolWorkflowInput
			if err := ctx.GetInput(&input); err != nil {
				return errorResult("failed to parse CallToolInput: %s", err), nil
			}

			// beforeCallTool middleware pipeline — may mutate arguments.
			arguments, err := runBeforeCallTool(ctx, &server, serverName, toolName, input.Arguments)
			if err != nil {
				// Return isError result (not a workflow failure) so the calling agent/LLM
				// receives a structured error it can act on — retry, pick another tool, or
				// inform the user — rather than a raw workflow failure with no content.
				return errorResult("beforeCallTool: %s", err), nil
			}

			// Convert structpb.Struct → map[string]any for the activity (MCP SDK needs a map).
			var argMap map[string]any
			if arguments != nil {
				argMap = arguments.AsMap()
			}

			actInput := activityCallToolInput{
				ToolName:  toolName,
				Arguments: argMap,
			}
			var result wfv1.CallMCPToolResponse
			t := ctx.CallActivity(mcpnames.MCPCallToolActivityName(serverName), task.WithActivityInput(actInput))
			if err := t.Await(&result); err != nil {
				errResult := errorResult("%s", err)
				final, hookErr := runAfterCallTool(ctx, &server, serverName, toolName, arguments, errResult)
				if hookErr != nil {
					return nil, fmt.Errorf("afterCallTool failed: %w", hookErr)
				}
				return final, nil
			}

			final, hookErr := runAfterCallTool(ctx, &server, serverName, toolName, arguments, &result)
			if hookErr != nil {
				return nil, fmt.Errorf("afterCallTool failed: %w", hookErr)
			}
			return final, nil

		default:
			return nil, fmt.Errorf("unknown MCP workflow name %q: expected suffix %q or %q",
				name, mcpnames.MCPMethodListTools, mcpnames.MCPMethodCallTool)
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
			// Disable the standalone SSE GET stream. The SDK sends GET / for
			// server-initiated notifications, but this blocks Client.Connect
			// synchronously (via sessionUpdated → connectStandaloneSSE) until
			// the HTTP client timeout fires if the server doesn't support it.
			// Dapr's MCP integration is request/response only (ListTools,
			// CallTool) and doesn't need server push.
			DisableStandaloneSSE: true,
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

// CallTimeout returns the per-call deadline for the given MCPServer.
// Falls back to defaultMCPTimeout when no timeout is configured.
func CallTimeout(server *mcpserverapi.MCPServer) time.Duration {
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

// stringDeref returns the dereferenced string or "" if nil.
func stringDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
