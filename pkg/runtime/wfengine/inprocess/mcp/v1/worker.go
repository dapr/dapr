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
	"errors"
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
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
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

// activityCallToolInput is the internal input passed from the orchestrator
// to the CallTool activity.
type activityCallToolInput struct {
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// RegisterMCP registers versioned MCP workflows for each known MCPServer.
// Safe to call multiple times — new servers are registered, existing ones skipped.
func RegisterMCP(registry *task.TaskRegistry, opts Options) error {
	for _, server := range opts.Store.ListMCPServers() {
		if err := RegisterMCPServer(registry, server, opts); err != nil {
			return err
		}
	}
	return nil
}

// RegisterMCPServer registers workflows and activities for a single MCPServer.
// Builds the HTTP client and MCP session eagerly - similar to component init().
// Returns an error if the server is unreachable or registration fails.
// Safe to call on hot-reload — registry upserts replace existing entries,
// and AddMCPServer invalidates stale cached clients/sessions.
func RegisterMCPServer(registry *task.TaskRegistry, server mcpserverapi.MCPServer, opts Options) error {
	session, err := getOrCreateSession(opts.Store, server.Name, &server, opts.Security)
	if err != nil {
		return fmt.Errorf("MCPServer %q: failed to connect: %w", server.Name, err)
	}

	orchestrator := makeOrchestrator(server, opts.Store)
	listActivity := makeListToolsActivity(server, session, opts)
	callActivity := makeCallToolActivity(server, session, opts)

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

// makeListToolsActivity returns a task.Activity that calls ListTools on the given MCP server.
func makeListToolsActivity(server mcpserverapi.MCPServer, session *mcp.ClientSession, opts Options) task.Activity {
	serverName := server.Name
	return func(ctx task.ActivityContext) (any, error) {
		callCtx := ctx.Context()
		timeout := callTimeout(&server)
		workerLog.Debugf("list-tools: MCPServer %q timeout=%s", serverName, timeout)
		callCtx, cancel := withDeadline(callCtx, timeout)
		defer cancel()

		workerLog.Debugf("list-tools: listing tools on %q", serverName)

		var tools []*wfv1.MCPToolDefinition
		var cursor string
		for {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			result, err := session.ListTools(callCtx, params)
			if err != nil {
				return &wfv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: MCP call failed for %q: %w", serverName, err)
			}

			for _, t := range result.Tools {
				td := &wfv1.MCPToolDefinition{
					Name:        t.Name,
					Description: &t.Description,
				}
				if t.InputSchema != nil {
					schema, ok := t.InputSchema.(map[string]any)
					if !ok {
						workerLog.Warnf("list-tools: tool %q on MCPServer %q has non-object inputSchema (type %T), skipping schema", t.Name, serverName, t.InputSchema)
					} else {
						s, err := structpb.NewStruct(schema)
						if err != nil {
							workerLog.Warnf("list-tools: tool %q on MCPServer %q: failed to convert inputSchema: %s", t.Name, serverName, err)
						} else {
							td.InputSchema = s
							if raw, err := json.Marshal(schema); err == nil {
								opts.Store.SetMCPToolSchema(serverName, t.Name, raw)
							}
						}
					}
				}
				tools = append(tools, td)
			}

			if result.NextCursor == "" {
				break
			}
			cursor = result.NextCursor
		}

		return &wfv1.ListMCPToolsResponse{Tools: tools}, nil
	}
}

// makeCallToolActivity returns a task.Activity that calls a tool on the given MCP server.
// Error strategy:
//   - Permanent errors (bad input, transport misconfiguration) are returned as
//     CallMCPToolResponse{IsError: true} so callers see a result, not a retry loop.
//   - Transient errors (secret store unavailable for OAuth2) are returned as activity-level
//     errors so the workflow engine retries the activity automatically.
//   - Error messages exposed to callers never include infrastructure details.
//
// makeCallToolActivity returns a task.Activity that calls a tool on the given MCP server.
func makeCallToolActivity(server mcpserverapi.MCPServer, session *mcp.ClientSession, opts Options) task.Activity {
	serverName := server.Name
	return func(ctx task.ActivityContext) (any, error) {
		var input activityCallToolInput
		if err := ctx.GetInput(&input); err != nil {
			return errorResult("call-tool: failed to parse input: %s", err), nil
		}

		callCtx := ctx.Context()
		timeout := callTimeout(&server)
		callCtx, cancel := withDeadline(callCtx, timeout)
		defer cancel()

		if validationErr := validateToolArguments(opts.Store, serverName, input.ToolName, input.Arguments); validationErr != "" {
			return errorResult("%s", validationErr), nil
		}

		argBytes, err := json.Marshal(input.Arguments)
		if err != nil {
			argBytes = []byte(fmt.Sprintf("failed to marshal arguments: %s for %v", err, input.Arguments))
		}
		workerLog.Debugf("call-tool: calling tool %q on MCPServer %q args %s", input.ToolName, serverName, argBytes)

		result, err := session.CallTool(callCtx, &mcp.CallToolParams{
			Name:      input.ToolName,
			Arguments: input.Arguments,
		})
		if err != nil {
			return errorResult("call-tool: MCP call failed for tool %q on %q: %s", input.ToolName, serverName, err), nil
		}

		return convertCallToolResult(result), nil
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

// getOrCreateSession returns a cached MCP session for the server, or creates
// and caches a new one. The session is connected with context.Background() so
// it outlives individual call contexts. Per-call deadlines are enforced by the
// individual ListTools/CallTool RPC calls, not the connection itself.
// If the cached session is stale (detected by a failed RPC), callers should
// call store.DeleteMCPSession and retry.
func getOrCreateSession(store *compstore.ComponentStore, serverName string, server *mcpserverapi.MCPServer, sec security.Handler) (*mcp.ClientSession, error) {
	// Fast path: return cached session if present.
	if cached := store.GetMCPSession(serverName); cached != nil {
		if session, ok := cached.(*mcp.ClientSession); ok {
			return session, nil
		}
	}

	// Build or retrieve the HTTP client
	httpClient := store.GetMCPHTTPClient(serverName)
	if httpClient == nil {
		built, err := mcpauth.BuildHTTPClient(context.Background(), server, store, sec, callTimeout(server))
		if err != nil {
			return nil, fmt.Errorf("failed to build HTTP client for %q: %w", serverName, err)
		}
		httpClient, _ = store.GetOrSetMCPHTTPClient(serverName, built)
	}

	// Build transport and connect.
	transport, err := buildTransport(server, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport for %q: %w", serverName, err)
	}

	workerLog.Debugf("connecting to MCP server %q", serverName)
	c := mcp.NewClient(&mcp.Implementation{Name: mcpClientName, Version: mcpClientVersion}, nil)
	session, err := c.Connect(context.Background(), transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MCP server %q: %w", serverName, err)
	}

	cached, stored := store.GetOrSetMCPSession(serverName, session)
	if !stored {
		session.Close()
		return cached.(*mcp.ClientSession), nil
	}
	return session, nil
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

// convertCallToolResult converts an mcp.CallToolResult from the go-sdk into a proto CallMCPToolResponse.
// Each MCP content type maps to the corresponding oneof variant in MCPContentBlock.
// Unknown/future content types are JSON-marshaled into a text block as a forward-compatible fallback.
func convertCallToolResult(r *mcp.CallToolResult) *wfv1.CallMCPToolResponse {
	out := &wfv1.CallMCPToolResponse{IsError: r.IsError}
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Text{
					Text: &wfv1.MCPTextContent{Text: v.Text},
				},
			})
		case *mcp.ImageContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Image{
					Image: &wfv1.MCPBinaryContent{MimeType: v.MIMEType, Data: v.Data},
				},
			})
		case *mcp.AudioContent:
			out.Content = append(out.Content, &wfv1.MCPContentBlock{
				Content: &wfv1.MCPContentBlock_Audio{
					Audio: &wfv1.MCPBinaryContent{MimeType: v.MIMEType, Data: v.Data},
				},
			})
		case *mcp.ResourceLink:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_ResourceLink{
						ResourceLink: &wfv1.MCPResourceContent{Resource: raw},
					},
				})
			}
		case *mcp.EmbeddedResource:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_EmbeddedResource{
						EmbeddedResource: &wfv1.MCPResourceContent{Resource: raw},
					},
				})
			}
		default:
			if b, err := json.Marshal(c); err == nil {
				out.Content = append(out.Content, &wfv1.MCPContentBlock{
					Content: &wfv1.MCPContentBlock_Text{
						Text: &wfv1.MCPTextContent{Text: string(b)},
					},
				})
			}
		}
	}
	return out
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

// validateToolArguments performs client-side validation of tool arguments
// against the tool's declared input schema (if known).
// It checks that all required properties are present.
// Returns an empty string when validation passes or no schema is available,
// or an error message describing the missing/invalid fields.
func validateToolArguments(store *compstore.ComponentStore, serverName, toolName string, args map[string]any) string {
	raw, ok := store.GetMCPToolSchema(serverName, toolName)
	if !ok || raw == nil {
		return ""
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return ""
	}

	requiredRaw, ok := schema[jsonFieldRequired]
	if !ok {
		return ""
	}

	requiredList, ok := requiredRaw.([]any)
	if !ok {
		return ""
	}

	var missing []string
	for _, r := range requiredList {
		name, ok := r.(string)
		if !ok {
			continue
		}
		if _, present := args[name]; !present {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return fmt.Sprintf("call-tool: missing required argument(s) for tool %q: %s",
			toolName, strings.Join(missing, ", "))
	}
	return ""
}

// stringDeref returns the dereferenced string or "" if nil.
func stringDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
