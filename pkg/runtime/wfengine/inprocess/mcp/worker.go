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
	"encoding/base64"
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
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
	mcptypes "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/types"
	"github.com/dapr/dapr/pkg/security"
)

var workerLog = logger.NewLogger("dapr.runtime.mcp.worker")

// Options configures the MCP in-process workflow subsystem.
type Options struct {
	// Store is required; it is used to look up MCPServer manifests and fetch secrets at call time.
	Store *compstore.ComponentStore
	// JWT enables SPIFFE workload identity JWT injection. If nil, SPIFFE is skipped.
	JWT security.JWTFetcher
}

const (
	// activityListTools is the fixed activity name for the ListTools transport call.
	activityListTools = "dapr.internal.mcp.list-tools"

	// activityCallTool is the fixed activity name for the CallTool transport call.
	activityCallTool = "dapr.internal.mcp.call-tool"

	// defaultMCPTimeout is the per-call deadline when no endpoint.timeout is set.
	defaultMCPTimeout = 30 * time.Second

	textContentType         = "text"
	imageContentType        = "image"
	audioContentType        = "audio"
	resourceLinkContentType = "resource_link"
	resourceContentType     = "resource"

	mcpClientName    = "dapr"
	mcpClientVersion = "v1alpha1"

	jsonFieldRequired = "required"
)

// activityListToolsInput is the internal input passed from the orchestrator
// to the ListTools activity. The MCPServerName is derived from the workflow
// name by the orchestrator — callers never set it.
type activityListToolsInput struct {
	MCPServerName string `json:"mcp_server_name"`
}

// activityCallToolInput is the internal input passed from the orchestrator
// to the CallTool activity. The MCPServerName is derived from the workflow
// name by the orchestrator — callers never set it.
type activityCallToolInput struct {
	MCPServerName string         `json:"mcp_server_name"`
	ToolName      string         `json:"tool_name"`
	Arguments     map[string]any `json:"arguments,omitempty"`
}

// RegisterMCP adds the MCP wildcard orchestrator and the two transport
// activities to an existing task.TaskRegistry. Returns an error if any
// registration fails — this indicates a programming error that should
// cause the runtime to shut down.
func RegisterMCP(registry *task.TaskRegistry, opts Options) error {
	if err := registry.AddWorkflowN("*", makeOrchestrator(opts.Store)); err != nil {
		return fmt.Errorf("failed to register MCP wildcard workflow: %w", err)
	}
	if err := registry.AddActivityN(activityListTools, makeListToolsActivity(opts)); err != nil {
		return fmt.Errorf("failed to register %s activity: %w", activityListTools, err)
	}
	if err := registry.AddActivityN(activityCallTool, makeCallToolActivity(opts)); err != nil {
		return fmt.Errorf("failed to register %s activity: %w", activityCallTool, err)
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
func makeOrchestrator(store *compstore.ComponentStore) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		name := ctx.Name

		switch {
		case strings.HasSuffix(name, mcptypes.MethodListTools):
			serverName := mcpServerName(name, mcptypes.MethodListTools)
			server, ok := store.GetMCPServer(serverName)
			if !ok {
				return &rtv1.ListMCPToolsResponse{}, fmt.Errorf("MCPServer %q not found", serverName)
			}

			// beforeListTools middleware pipeline
			if err := runBeforeListTools(ctx, &server, serverName); err != nil {
				return nil, errors.New("beforeListTools failed: " + err.Error())
			}

			// The server name is derived from the workflow name — not from caller input.
			actInput := activityListToolsInput{MCPServerName: serverName}
			var result rtv1.ListMCPToolsResponse
			t := ctx.CallActivity(activityListTools, task.WithActivityInput(actInput))
			if err := t.Await(&result); err != nil {
				return nil, errors.New("list-tools activity failed: " + err.Error())
			}

			final, err := runAfterListTools(ctx, &server, serverName, &result)
			if err != nil {
				return nil, fmt.Errorf("afterListTools failed: %w", err)
			}
			return final, nil

		case strings.HasSuffix(name, mcptypes.MethodCallTool):
			serverName := mcpServerName(name, mcptypes.MethodCallTool)
			server, ok := store.GetMCPServer(serverName)
			if !ok {
				return errorResult("MCPServer %q not found", serverName), nil
			}

			var input rtv1.MCPCallToolWorkflowInput
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

			// The server name is derived from the workflow name — not from caller input.
			actInput := activityCallToolInput{
				MCPServerName: serverName,
				ToolName:      input.ToolName,
				Arguments:     argMap,
			}
			var result rtv1.CallMCPToolResponse
			t := ctx.CallActivity(activityCallTool, task.WithActivityInput(actInput))
			if err := t.Await(&result); err != nil {
				// Activity-level failure: return as CallMCPToolResponse{IsError: true},
				// not as a workflow exception.
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

// errorResult returns a CallMCPToolResponse with is_error=true and a single text content item.
func errorResult(format string, args ...any) *rtv1.CallMCPToolResponse {
	return &rtv1.CallMCPToolResponse{
		IsError: true,
		Content: []*rtv1.MCPContentItem{{
			Type: textContentType,
			Text: fmt.Sprintf(format, args...),
		}},
	}
}

// mcpServerName extracts the MCPServer resource name from a workflow name
// of the form "dapr.internal.mcp.<name>.<method>".
func mcpServerName(workflowName, method string) string {
	trimmed := strings.TrimPrefix(workflowName, mcptypes.WorkflowNamePrefix)
	return strings.TrimSuffix(trimmed, method)
}

// makeListToolsActivity returns a task.Activity that calls ListTools on the
// named MCP server and returns a *rtv1.ListMCPToolsResponse.
func makeListToolsActivity(opts Options) task.Activity {
	return func(ctx task.ActivityContext) (any, error) {
		var input activityListToolsInput
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("list-tools: failed to parse input: %w", err)
		}

		server, ok := opts.Store.GetMCPServer(input.MCPServerName)
		if !ok {
			return &rtv1.ListMCPToolsResponse{}, fmt.Errorf("MCPServer %q not found", input.MCPServerName)
		}

		callCtx := ctx.Context()
		timeout := callTimeout(&server)
		workerLog.Debugf("list-tools: MCPServer %q timeout=%s", input.MCPServerName, timeout)
		callCtx, cancel := withDeadline(callCtx, timeout)
		defer cancel()

		httpClient := opts.Store.GetMCPHTTPClient(input.MCPServerName)
		if httpClient == nil {
			var err error
			httpClient, err = mcpauth.BuildHTTPClient(callCtx, &server, opts.Store, opts.JWT, timeout)
			if err != nil {
				return &rtv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: failed to build HTTP client for %q: %w", input.MCPServerName, err)
			}
			opts.Store.SetMCPHTTPClient(input.MCPServerName, httpClient)
		}

		session, err := getOrCreateSession(opts.Store, input.MCPServerName, &server, httpClient)
		if err != nil {
			return &rtv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: %w", err)
		}

		workerLog.Debugf("list-tools: listing tools on %q", input.MCPServerName)

		// Paginate through all available tools. The MCP spec allows servers
		// to return tools in pages with a cursor; we collect all pages.
		var tools []*rtv1.MCPToolDefinition
		var cursor string
		for {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			result, err := session.ListTools(callCtx, params)
			if err != nil {
				return &rtv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: MCP call failed for %q: %w", input.MCPServerName, err)
			}

			for _, t := range result.Tools {
				td := &rtv1.MCPToolDefinition{
					Name:        t.Name,
					Description: t.Description,
				}
				if t.InputSchema != nil {
					if schema, ok := t.InputSchema.(map[string]any); ok {
						if raw, err := json.Marshal(schema); err == nil {
							td.InputSchema = raw
							opts.Store.SetMCPToolSchema(input.MCPServerName, t.Name, raw)
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

		return &rtv1.ListMCPToolsResponse{Tools: tools}, nil
	}
}

// makeCallToolActivity returns a task.Activity that calls a tool on the named
// MCP server and returns a *rtv1.CallMCPToolResponse.
//
// Error strategy:
//   - Permanent errors (bad input, unknown server, transport misconfiguration) are
//     returned as CallMCPToolResponse{IsError: true} so callers see a result, not a retry loop.
//   - Transient errors (secret store unavailable for OAuth2) are returned as activity-level
//     errors so the workflow engine retries the activity automatically.
//   - Error messages exposed to callers never include infrastructure details (secret store
//     names, key names, internal URLs). Details are logged server-side only.
func makeCallToolActivity(opts Options) task.Activity {
	return func(ctx task.ActivityContext) (any, error) {
		var input activityCallToolInput
		if err := ctx.GetInput(&input); err != nil {
			return errorResult("call-tool: failed to parse input: %s", err), nil
		}

		server, ok := opts.Store.GetMCPServer(input.MCPServerName)
		if !ok {
			return errorResult("MCPServer %q not found", input.MCPServerName), nil
		}

		callCtx := ctx.Context()
		timeout := callTimeout(&server)
		callCtx, cancel := withDeadline(callCtx, timeout)
		defer cancel()

		httpClient := opts.Store.GetMCPHTTPClient(input.MCPServerName)
		if httpClient == nil {
			var err error
			httpClient, err = mcpauth.BuildHTTPClient(callCtx, &server, opts.Store, opts.JWT, timeout)
			if err != nil {
				// Secret fetch failures are transient — return an activity error so
				// the workflow engine retries. Log details server-side only.
				if mcpauth.IsSecretFetchError(err) {
					workerLog.Warnf("call-tool: transient auth error for MCPServer %q: %s", input.MCPServerName, err)
					return nil, fmt.Errorf("call-tool: temporary authentication failure for MCPServer %q; retrying", input.MCPServerName)
				}
				return errorResult("call-tool: authentication configuration error for MCPServer %q", input.MCPServerName), nil
			}
			opts.Store.SetMCPHTTPClient(input.MCPServerName, httpClient)
		}

		// Validate tool arguments against the cached input schema if available.
		if validationErr := validateToolArguments(opts.Store, input.MCPServerName, input.ToolName, input.Arguments); validationErr != "" {
			return errorResult("%s", validationErr), nil
		}

		session, err := getOrCreateSession(opts.Store, input.MCPServerName, &server, httpClient)
		if err != nil {
			return errorResult("call-tool: %s", err), nil
		}

		argBytes, err := json.Marshal(input.Arguments)
		if err != nil {
			argBytes = []byte(fmt.Sprintf("failed to marshal arguments: %s for %v", err, input.Arguments))
		}
		workerLog.Debugf("call-tool: calling tool %q on MCPServer %q args %s", input.ToolName, input.MCPServerName, argBytes)

		result, err := session.CallTool(callCtx, &mcp.CallToolParams{
			Name:      input.ToolName,
			Arguments: input.Arguments,
		})
		if err != nil {
			return errorResult("call-tool: MCP call failed for tool %q on %q: %s", input.ToolName, input.MCPServerName, err), nil
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
func getOrCreateSession(store *compstore.ComponentStore, serverName string, server *mcpserverapi.MCPServer, httpClient *http.Client) (*mcp.ClientSession, error) {
	if cached := store.GetMCPSession(serverName); cached != nil {
		if session, ok := cached.(*mcp.ClientSession); ok {
			return session, nil
		}
	}

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

	store.SetMCPSession(serverName, session)
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
// The response shape matches the MCP protocol's CallToolResult for wire compatibility
// with MCP-native clients (Claude, Cursor, etc.).
//
// Handles all MCP content types defined in the spec:
//   - text          → Text field
//   - image         → Data (base64) + MimeType
//   - audio         → Data (base64) + MimeType
//   - resource_link → Resource (raw JSON preserving all fields)
//   - resource      → Resource (raw JSON preserving embedded resource)
//
// Unknown/future content types are JSON-marshaled into a text item as a forward-compatible fallback.
func convertCallToolResult(r *mcp.CallToolResult) *rtv1.CallMCPToolResponse {
	out := &rtv1.CallMCPToolResponse{IsError: r.IsError}
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			out.Content = append(out.Content, &rtv1.MCPContentItem{Type: textContentType, Text: v.Text})
		case *mcp.ImageContent:
			out.Content = append(out.Content, &rtv1.MCPContentItem{
				Type: imageContentType, Data: base64.StdEncoding.EncodeToString(v.Data), MimeType: v.MIMEType,
			})
		case *mcp.AudioContent:
			out.Content = append(out.Content, &rtv1.MCPContentItem{
				Type: audioContentType, Data: base64.StdEncoding.EncodeToString(v.Data), MimeType: v.MIMEType,
			})
		case *mcp.ResourceLink:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &rtv1.MCPContentItem{Type: resourceLinkContentType, Resource: raw})
			} else {
				out.Content = append(out.Content, &rtv1.MCPContentItem{Type: textContentType, Text: fmt.Sprintf("failed to marshal resource_link: %s", err)})
			}
		case *mcp.EmbeddedResource:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, &rtv1.MCPContentItem{Type: resourceContentType, Resource: raw})
			} else {
				out.Content = append(out.Content, &rtv1.MCPContentItem{Type: textContentType, Text: fmt.Sprintf("failed to marshal embedded resource: %s", err)})
			}
		default:
			if b, err := json.Marshal(c); err == nil {
				out.Content = append(out.Content, &rtv1.MCPContentItem{Type: textContentType, Text: string(b)})
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
