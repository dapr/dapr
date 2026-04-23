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

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
	"github.com/dapr/durabletask-go/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"
)

// activityCallToolInput is the internal input passed from the orchestrator
// to the CallTool activity.
type activityCallToolInput struct {
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// makeListToolsActivity returns a task.Activity that calls ListTools on the given MCP server.
func makeListToolsActivity(server mcpserverapi.MCPServer, session *mcp.ClientSession, schemas *toolSchemaCache) task.Activity {
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
								schemas.set(t.Name, raw)
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
// The session and schema cache are captured at registration time; hot-reload replaces the closure.
func makeCallToolActivity(server mcpserverapi.MCPServer, session *mcp.ClientSession, schemas *toolSchemaCache, opts Options) task.Activity {
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

		if validationErr := validateToolArguments(schemas, input.ToolName, input.Arguments); validationErr != "" {
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
