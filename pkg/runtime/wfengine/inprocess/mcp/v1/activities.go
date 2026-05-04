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
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/durabletask-go/task"
)

// activityCallToolInput is the internal input passed from the orchestrator
// to the CallTool activity.
type activityCallToolInput struct {
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// makeListToolsActivity returns a task.Activity that calls ListTools on the given MCP server.
// The SessionHolder handles reconnection if the connection drops.
// listCache is checked first — if pre-populated by eager discovery during RegisterMCPServer,
// the activity returns the cached result immediately without an upstream MCP call.
func makeListToolsActivity(server mcpserverapi.MCPServer, holder *SessionHolder, schemas *toolSchemaCache, listCache *toolListCache) task.Activity {
	serverName := server.Name
	return func(ctx task.ActivityContext) (any, error) {
		// Cache hit: return pre-discovered tools without upstream call.
		if cached, ok := listCache.load(); ok {
			workerLog.Debugf("list-tools: MCPServer %q served from cache (%d tools)", serverName, len(cached))
			return &wfv1.ListMCPToolsResponse{Tools: cached}, nil
		}

		baseCtx := ctx.Context()
		timeout := CallTimeout(&server)
		workerLog.Debugf("list-tools: MCPServer %q per-page timeout=%s", serverName, timeout)

		// Use the per-page timeout for the initial Session() call.
		sessionCtx, sessionCancel := withDeadline(baseCtx, timeout)
		session, err := holder.Session(sessionCtx)
		sessionCancel()
		if err != nil {
			return &wfv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: %w", err)
		}

		const maxListToolsPages = 500

		var tools []*wfv1.MCPToolDefinition
		var cursor string
		for range maxListToolsPages {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			// Apply the timeout per network call rather than across the whole pagination loop.
			pageCtx, pageCancel := withDeadline(baseCtx, timeout)
			result, err := session.ListTools(pageCtx, params)
			if err != nil {
				if isConnectionClosed(err) {
					workerLog.Warnf("list-tools: connection lost for %q, reconnecting", serverName)
					session, err = holder.Reconnect(pageCtx)
					if err != nil {
						pageCancel()
						return &wfv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: reconnect failed for %q: %w", serverName, err)
					}
					result, err = session.ListTools(pageCtx, params)
				}
				if err != nil {
					pageCancel()
					return &wfv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: MCP call failed for %q: %w", serverName, err)
				}
			}
			pageCancel()

			for _, t := range result.Tools {
				td := &wfv1.MCPToolDefinition{
					Name: t.Name,
				}
				if t.Description != "" {
					td.Description = &t.Description
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
								if err := schemas.set(t.Name, raw); err != nil {
									workerLog.Warnf("list-tools: tool %q on MCPServer %q: %s", t.Name, serverName, err)
								}
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
		if cursor != "" {
			workerLog.Warnf("list-tools: MCPServer %q returned more than %d pages of tools; results truncated", serverName, maxListToolsPages)
		}

		return &wfv1.ListMCPToolsResponse{Tools: tools}, nil
	}
}

// makeCallToolActivity returns a task.Activity that calls a tool on the given MCP server.
func makeCallToolActivity(server mcpserverapi.MCPServer, holder *SessionHolder, schemas *toolSchemaCache, opts Options) task.Activity {
	serverName := server.Name
	return func(ctx task.ActivityContext) (any, error) {
		var input activityCallToolInput
		if err := ctx.GetInput(&input); err != nil {
			return errorResult("call-tool: failed to parse input: %s", err), nil
		}

		callCtx := ctx.Context()
		timeout := CallTimeout(&server)
		callCtx, cancel := withDeadline(callCtx, timeout)
		defer cancel()

		if validationErr := validateToolArguments(schemas, input.ToolName, input.Arguments); validationErr != "" {
			return errorResult("%s", validationErr), nil
		}

		session, err := holder.Session(callCtx)
		if err != nil {
			return errorResult("call-tool: %s", err), nil
		}

		workerLog.Debugf("call-tool: calling tool %q on MCPServer %q (%d argument keys)", input.ToolName, serverName, len(input.Arguments))

		result, err := session.CallTool(callCtx, &mcp.CallToolParams{
			Name:      input.ToolName,
			Arguments: input.Arguments,
		})
		if err != nil {
			if isConnectionClosed(err) {
				workerLog.Warnf("call-tool: connection lost for %q, reconnecting", serverName)
				session, err = holder.Reconnect(callCtx)
				if err != nil {
					return errorResult("call-tool: reconnect failed for %q: %s", serverName, err), nil
				}
				result, err = session.CallTool(callCtx, &mcp.CallToolParams{
					Name:      input.ToolName,
					Arguments: input.Arguments,
				})
			}
			if err != nil {
				return errorResult("call-tool: MCP call failed for tool %q on %q: %s", input.ToolName, serverName, err), nil
			}
		}

		return convertCallToolResult(result), nil
	}
}
