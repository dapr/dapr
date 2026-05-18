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
	"time"

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

// listToolsPage runs one paginated ListTools call with a per-call timeout and
// a single connection-closed retry.
func listToolsPage(
	baseCtx context.Context,
	timeout time.Duration,
	holder *SessionHolder,
	serverName string,
	params *mcp.ListToolsParams,
) (*mcp.ListToolsResult, error) {
	pageCtx, pageCancel := withDeadline(baseCtx, timeout)
	defer pageCancel()

	session, err := holder.Session(pageCtx)
	if err != nil {
		return nil, fmt.Errorf("list-tools: session for %q: %w", serverName, err)
	}
	result, err := session.ListTools(pageCtx, params)
	if err == nil {
		return result, nil
	}
	if !isConnectionClosed(err) {
		return nil, fmt.Errorf("list-tools: initial call failed for %q: %w", serverName, err)
	}

	workerLog.Warnf("list-tools: connection lost for %q, reconnecting", serverName)
	session, err = holder.Reconnect(pageCtx)
	if err != nil {
		return nil, fmt.Errorf("list-tools: reconnect failed for %q: %w", serverName, err)
	}
	result, err = session.ListTools(pageCtx, params)
	if err != nil {
		return nil, fmt.Errorf("list-tools: retry call failed for %q after reconnect: %w", serverName, err)
	}
	return result, nil
}

// callToolOnce runs one CallTool with a single connection-closed retry.
func callToolOnce(
	callCtx context.Context,
	holder *SessionHolder,
	serverName string,
	params *mcp.CallToolParams,
) (*mcp.CallToolResult, error) {
	session, err := holder.Session(callCtx)
	if err != nil {
		return nil, fmt.Errorf("call-tool: session for %q: %w", serverName, err)
	}
	result, err := session.CallTool(callCtx, params)
	if err == nil {
		return result, nil
	}
	if !isConnectionClosed(err) {
		return nil, fmt.Errorf("call-tool: initial call failed for %q: %w", serverName, err)
	}

	workerLog.Warnf("call-tool: connection lost for %q, reconnecting", serverName)
	session, err = holder.Reconnect(callCtx)
	if err != nil {
		return nil, fmt.Errorf("call-tool: reconnect failed for %q: %w", serverName, err)
	}
	result, err = session.CallTool(callCtx, params)
	if err != nil {
		return nil, fmt.Errorf("call-tool: retry call failed for %q after reconnect: %w", serverName, err)
	}
	return result, nil
}

// toolDefinitionFromMCP converts a single mcp.Tool into a wfv1.MCPToolDefinition,
// converting the input schema and populating the schema cache. Returns an error
// for any malformed schema; callers should fail the parent activity.
func toolDefinitionFromMCP(t *mcp.Tool, serverName string, schemas *toolSchemaCache) (*wfv1.MCPToolDefinition, error) {
	td := &wfv1.MCPToolDefinition{
		Name: t.Name,
	}
	if t.Description != "" {
		td.Description = &t.Description
	}
	if t.InputSchema == nil {
		return td, nil
	}
	schema, ok := t.InputSchema.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("list-tools: tool %q on MCPServer %q has non-object inputSchema (type %T)", t.Name, serverName, t.InputSchema)
	}
	s, err := structpb.NewStruct(schema)
	if err != nil {
		return nil, fmt.Errorf("list-tools: tool %q on MCPServer %q: failed to convert inputSchema: %w", t.Name, serverName, err)
	}
	td.InputSchema = s
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("list-tools: tool %q on MCPServer %q: failed to marshal inputSchema: %w", t.Name, serverName, err)
	}
	if err := schemas.set(t.Name, raw); err != nil {
		return nil, fmt.Errorf("list-tools: tool %q on MCPServer %q: %w", t.Name, serverName, err)
	}
	return td, nil
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

		var tools []*wfv1.MCPToolDefinition
		var cursor string
		for range maxListToolsPages {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			result, err := listToolsPage(baseCtx, timeout, holder, serverName, params)
			if err != nil {
				return &wfv1.ListMCPToolsResponse{}, err
			}

			for _, t := range result.Tools {
				td, err := toolDefinitionFromMCP(t, serverName, schemas)
				if err != nil {
					return &wfv1.ListMCPToolsResponse{}, err
				}
				tools = append(tools, td)
			}

			if result.NextCursor == "" {
				break
			}
			cursor = result.NextCursor
		}
		if cursor != "" {
			return &wfv1.ListMCPToolsResponse{}, fmt.Errorf("list-tools: MCPServer %q returned more than %d pages of tools", serverName, maxListToolsPages)
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

		workerLog.Debugf("call-tool: calling tool %q on MCPServer %q (%d argument keys)", input.ToolName, serverName, len(input.Arguments))

		result, err := callToolOnce(callCtx, holder, serverName, &mcp.CallToolParams{
			Name:      input.ToolName,
			Arguments: input.Arguments,
		})
		if err != nil {
			return errorResult("%s (tool %q)", err, input.ToolName), nil
		}

		return convertCallToolResult(result), nil
	}
}
