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

	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/kit/logger"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	mcpauth "github.com/dapr/dapr/pkg/runtime/mcp/auth"
	. "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/types"
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
//   - beforeCallTool is awaited; any error aborts with CallToolResult{IsError:true}.
//   - dapr.internal.mcp.call-tool activity errors are returned as CallToolResult{IsError:true}.
//   - afterCallTool hooks are awaited; errors are logged but do not affect the result.
func makeOrchestrator(store *compstore.ComponentStore) func(*task.WorkflowContext) (any, error) {
	return func(ctx *task.WorkflowContext) (any, error) {
		name := ctx.Name

		switch {
		case strings.HasSuffix(name, MethodListTools):
			serverName := mcpServerName(name, MethodListTools)
			server, ok := store.GetMCPServer(serverName)
			if !ok {
				return &ListToolsResult{}, fmt.Errorf("MCPServer %q not found", serverName)
			}

			var input ListToolsInput
			if err := ctx.GetInput(&input); err != nil {
				return nil, errors.New("failed to parse ListToolsInput: " + err.Error())
			}
			if input.MCPServerName == "" {
				input.MCPServerName = serverName
			}

			// beforeListTools middleware pipeline
			if err := runBeforeListTools(ctx, &server, serverName); err != nil {
				return nil, errors.New("beforeListTools failed: " + err.Error())
			}

			var result ListToolsResult
			t := ctx.CallActivity(activityListTools, task.WithActivityInput(input))
			if err := t.Await(&result); err != nil {
				runAfterListTools(ctx, &server, serverName, &CallToolResult{
					IsError: true, Content: []ContentItem{{Type: textContentType, Text: err.Error()}},
				})
				return nil, errors.New("list-tools activity failed: " + err.Error())
			}

			runAfterListTools(ctx, &server, serverName, result)
			return result, nil

		case strings.HasSuffix(name, MethodCallTool):
			serverName := mcpServerName(name, MethodCallTool)
			server, ok := store.GetMCPServer(serverName)
			if !ok {
				return &CallToolResult{}, fmt.Errorf("MCPServer %q not found", serverName)
			}

			var input CallToolInput
			if err := ctx.GetInput(&input); err != nil {
				return &CallToolResult{IsError: true, Content: []ContentItem{{
					Type: textContentType,
					Text: fmt.Sprintf("failed to parse CallToolInput: %s", err),
				}}}, nil
			}
			if input.MCPServerName == "" {
				input.MCPServerName = serverName
			}
			if input.ToolName == "" {
				return nil, fmt.Errorf("CallTool requires a non-empty toolName")
			}

			// beforeCallTool middleware pipeline
			if err := runBeforeCallTool(ctx, &server, serverName, input.ToolName, input.Arguments); err != nil {
				return &CallToolResult{IsError: true, Content: []ContentItem{{
					Type: textContentType,
					Text: fmt.Sprintf("beforeCallTool: %s", err),
				}}}, nil
			}

			var result CallToolResult
			t := ctx.CallActivity(activityCallTool, task.WithActivityInput(input))
			if err := t.Await(&result); err != nil {
				// Activity-level failure: return as CallToolResult{isError: true},
				// not as a workflow exception.
				errResult := &CallToolResult{IsError: true, Content: []ContentItem{{
					Type: textContentType,
					Text: err.Error(),
				}}}
				runAfterCallTool(ctx, &server, serverName, input.ToolName, input.Arguments, errResult)
				return errResult, nil
			}

			runAfterCallTool(ctx, &server, serverName, input.ToolName, input.Arguments, result)
			return result, nil

		default:
			return nil, fmt.Errorf("unknown MCP workflow name %q: expected suffix %q or %q",
				name, MethodListTools, MethodCallTool)
		}
	}
}

// mcpServerName extracts the MCPServer resource name from a workflow name
// of the form "dapr.internal.mcp.<name>.<method>".
func mcpServerName(workflowName, method string) string {
	trimmed := strings.TrimPrefix(workflowName, WorkflowNamePrefix)
	return strings.TrimSuffix(trimmed, method)
}

// makeListToolsActivity returns a task.Activity that calls ListTools on the
// named MCP server and returns a ListToolsResult.
func makeListToolsActivity(opts Options) task.Activity {
	return func(ctx task.ActivityContext) (any, error) {
		var input ListToolsInput
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("list-tools: failed to parse input: %w", err)
		}

		server, ok := opts.Store.GetMCPServer(input.MCPServerName)
		if !ok {
			return &ListToolsResult{}, fmt.Errorf("MCPServer %q not found", input.MCPServerName)
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
				return &ListToolsResult{}, fmt.Errorf("list-tools: failed to build HTTP client for %q: %w", input.MCPServerName, err)
			}
			opts.Store.SetMCPHTTPClient(input.MCPServerName, httpClient)
		}

		transport, err := buildTransport(&server, httpClient)
		if err != nil {
			return &ListToolsResult{}, fmt.Errorf("list-tools: failed to build transport for %q: %w", input.MCPServerName, err)
		}

		workerLog.Debugf("list-tools: connecting to MCP server %q", input.MCPServerName)
		c := mcp.NewClient(&mcp.Implementation{Name: mcpClientName, Version: mcpClientVersion}, nil)
		session, err := c.Connect(callCtx, transport, nil)
		if err != nil {
			return &ListToolsResult{}, fmt.Errorf("list-tools: failed to connect to MCP server %q: %w", input.MCPServerName, err)
		}
		defer session.Close()

		// The MCP SDK v1.2.0 detaches the connection context from callCtx,
		// so context deadlines do not propagate to the underlying SSE stream.
		// Enforce our own timeout by closing the session if the deadline fires.
		timer := time.AfterFunc(timeout, func() {
			workerLog.Warnf("list-tools: timeout (%s) reached for MCPServer %q, closing session", timeout, input.MCPServerName)
			session.Close()
		})
		defer timer.Stop()

		workerLog.Debugf("list-tools: connected, listing tools on %q", input.MCPServerName)

		// Paginate through all available tools. The MCP spec allows servers
		// to return tools in pages with a cursor; we collect all pages.
		var tools []ToolDefinition
		var cursor string
		for {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			result, err := session.ListTools(callCtx, params)
			if err != nil {
				return &ListToolsResult{}, fmt.Errorf("list-tools: MCP call failed for %q: %w", input.MCPServerName, err)
			}

			for _, t := range result.Tools {
				td := ToolDefinition{
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
		timer.Stop()

		return &ListToolsResult{Tools: tools}, nil
	}
}

// makeCallToolActivity returns a task.Activity that calls a tool on the named
// MCP server and returns a CallToolResult.
//
// Error strategy:
//   - Permanent errors (bad input, unknown server, transport misconfiguration) are
//     returned as CallToolResult{IsError: true} so callers see a result, not a retry loop.
//   - Transient errors (secret store unavailable for OAuth2) are returned as activity-level
//     errors so the workflow engine retries the activity automatically.
//   - Error messages exposed to callers never include infrastructure details (secret store
//     names, key names, internal URLs). Details are logged server-side only.
func makeCallToolActivity(opts Options) task.Activity {
	return func(ctx task.ActivityContext) (any, error) {
		var input CallToolInput
		if err := ctx.GetInput(&input); err != nil {
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: fmt.Sprintf("call-tool: failed to parse input: %s", err),
			}}}, nil
		}

		server, ok := opts.Store.GetMCPServer(input.MCPServerName)
		if !ok {
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: fmt.Sprintf("MCPServer %q not found", input.MCPServerName),
			}}}, nil
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
				return &CallToolResult{IsError: true, Content: []ContentItem{{
					Type: textContentType,
					Text: fmt.Sprintf("call-tool: authentication configuration error for MCPServer %q", input.MCPServerName),
				}}}, nil
			}
			opts.Store.SetMCPHTTPClient(input.MCPServerName, httpClient)
		}

		transport, err := buildTransport(&server, httpClient)
		if err != nil {
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: fmt.Sprintf("call-tool: failed to build transport for %q: %s", input.MCPServerName, err),
			}}}, nil
		}

		// Validate tool arguments against the cached input schema if available.
		if validationErr := validateToolArguments(opts.Store, input.MCPServerName, input.ToolName, input.Arguments); validationErr != "" {
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: validationErr,
			}}}, nil
		}

		c := mcp.NewClient(&mcp.Implementation{Name: mcpClientName, Version: mcpClientVersion}, nil)
		session, err := c.Connect(callCtx, transport, nil)
		if err != nil {
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: fmt.Sprintf("call-tool: failed to connect to MCP server %q: %s", input.MCPServerName, err),
			}}}, nil
		}
		defer session.Close()

		// The MCP SDK detaches the connection context, so enforce
		// our own timeout by closing the session if the deadline fires.
		timer := time.AfterFunc(timeout, func() {
			workerLog.Warnf("call-tool: timeout (%s) reached for MCPServer %q, closing session", timeout, input.MCPServerName)
			session.Close()
		})
		defer timer.Stop()

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
			return &CallToolResult{IsError: true, Content: []ContentItem{{
				Type: textContentType,
				Text: fmt.Sprintf("call-tool: MCP call failed for tool %q on %q: %s", input.ToolName, input.MCPServerName, err),
			}}}, nil
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

// convertCallToolResult converts an mcp.CallToolResult from the go-sdk into our internal CallToolResult type.
// Handles all MCP content types defined in the spec:
//   - text          → Text field
//   - image         → Data (base64) + MimeType
//   - audio         → Data (base64) + MimeType
//   - resource_link → Resource (raw JSON preserving all fields)
//   - resource      → Resource (raw JSON preserving embedded resource)
//
// Unknown/future content types are JSON-marshaled into a text item as a forward-compatible fallback.
func convertCallToolResult(r *mcp.CallToolResult) *CallToolResult {
	out := &CallToolResult{IsError: r.IsError}
	for _, c := range r.Content {
		switch v := c.(type) {
		case *mcp.TextContent:
			out.Content = append(out.Content, ContentItem{Type: textContentType, Text: v.Text})
		case *mcp.ImageContent:
			// v.Data is raw bytes (Go's JSON unmarshaler decoded the base64 wire format).
			// Re-encode to base64 for our JSON output.
			out.Content = append(out.Content, ContentItem{
				Type: imageContentType, Data: base64.StdEncoding.EncodeToString(v.Data), MimeType: v.MIMEType,
			})
		case *mcp.AudioContent:
			out.Content = append(out.Content, ContentItem{
				Type: audioContentType, Data: base64.StdEncoding.EncodeToString(v.Data), MimeType: v.MIMEType,
			})
		case *mcp.ResourceLink:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, ContentItem{Type: resourceLinkContentType, Resource: raw})
			} else {
				out.Content = append(out.Content, ContentItem{Type: textContentType, Text: fmt.Sprintf("failed to marshal resource_link: %s", err)})
			}
		case *mcp.EmbeddedResource:
			if raw, err := json.Marshal(v); err == nil {
				out.Content = append(out.Content, ContentItem{Type: resourceContentType, Resource: raw})
			} else {
				out.Content = append(out.Content, ContentItem{Type: textContentType, Text: fmt.Sprintf("failed to marshal embedded resource: %s", err)})
			}
		default:
			// Unknown/future content type: marshal as JSON text as forward-compatible fallback.
			if b, err := json.Marshal(c); err == nil {
				out.Content = append(out.Content, ContentItem{Type: textContentType, Text: string(b)})
			}
		}
	}
	return out
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
