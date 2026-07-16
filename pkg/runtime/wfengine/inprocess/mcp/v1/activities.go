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

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	mcptypes "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/v1/types"
	"github.com/dapr/durabletask-go/task"
)

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

// cacheToolSchema marshals a tool's input schema,
// and stores it in the schema cache for later argument validation.
// Errors are best-effort.
// Failure to cache a tool's schema only disables client-side argument validation,
// and the tool itself remains callable.
func cacheToolSchema(t *mcp.Tool, schemas *toolSchemaCache) {
	if t.InputSchema == nil {
		return
	}
	raw, err := json.Marshal(t.InputSchema)
	if err != nil {
		return
	}
	_ = schemas.set(t.Name, raw)
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
			return &mcp.ListToolsResult{Tools: cached}, nil
		}

		baseCtx := ctx.Context()
		timeout := CallTimeout(&server)
		workerLog.Debugf("list-tools: MCPServer %q per-page timeout=%s", serverName, timeout)

		var tools []*mcp.Tool
		var cursor string
		for range maxListToolsPages {
			params := &mcp.ListToolsParams{}
			if cursor != "" {
				params.Cursor = cursor
			}
			result, err := listToolsPage(baseCtx, timeout, holder, serverName, params)
			if err != nil {
				return &mcp.ListToolsResult{}, err
			}

			for _, t := range result.Tools {
				cacheToolSchema(t, schemas)
				tools = append(tools, t)
			}

			if result.NextCursor == "" {
				break
			}
			cursor = result.NextCursor
		}
		if cursor != "" {
			return &mcp.ListToolsResult{}, fmt.Errorf("list-tools: MCPServer %q returned more than %d pages of tools", serverName, maxListToolsPages)
		}

		return &mcp.ListToolsResult{Tools: tools}, nil
	}
}

// makeCallToolActivity returns a task.Activity that calls a tool on the given MCP server.
func makeCallToolActivity(server mcpserverapi.MCPServer, holder *SessionHolder, schemas *toolSchemaCache, opts Options) task.Activity {
	serverName := server.Name
	return func(ctx task.ActivityContext) (any, error) {
		var input mcptypes.CallToolActivityInput
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

		return result, nil
	}
}
