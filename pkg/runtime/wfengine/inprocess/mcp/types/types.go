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

package types

import "encoding/json"

// ListToolsInput is the input payload for a ListTools orchestration.
// The MCPServer name is derived from the workflow name, not from this input.
type ListToolsInput struct{}

// ListToolsResult is the output of a ListTools orchestration.
// Field names and types align with ListMCPToolsResponse in mcp.proto.
type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolDefinition describes a single tool advertised by the MCP server.
// Field names and types align with MCPToolDefinition in mcp.proto.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// CallToolInput is the input payload for a CallTool orchestration.
// The MCPServer name is derived from the workflow name, not from this input.
// Arguments is map[string]any (not bytes) because the MCP SDK and
// the validation code need parsed access to individual argument keys.
type CallToolInput struct {
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult is the output of a CallTool orchestration.
// Field names and types align with CallMCPToolResponse in mcp.proto.
type CallToolResult struct {
	IsError bool          `json:"is_error"`
	Content []ContentItem `json:"content,omitempty"`
}

// ContentItem is a single piece of content returned by a tool call.
// Field names and types align with MCPContentItem in mcp.proto.
type ContentItem struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MimeType string          `json:"mime_type,omitempty"`
	Resource json.RawMessage `json:"resource,omitempty"`
}

// BeforeCallInput is passed to beforeCallTool / beforeListTools middleware hooks.
// ToolName is empty for ListTools orchestrations.
// TODO: add to mcp.proto so other SDKs can deserialize middleware payloads.
type BeforeCallInput struct {
	MCPServerName string         `json:"mcp_server_name"`
	ToolName      string         `json:"tool_name,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
}

// AfterCallInput is passed to afterCallTool / afterListTools middleware hooks.
// ToolName is empty for ListTools orchestrations.
// TODO: add to mcp.proto so other SDKs can deserialize middleware payloads.
type AfterCallInput struct {
	MCPServerName string         `json:"mcp_server_name"`
	ToolName      string         `json:"tool_name,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	Result        any            `json:"result"`
}

// WorkflowNamePrefix is the prefix shared by all built-in MCP orchestrations.
const WorkflowNamePrefix = "dapr.internal.mcp."

// MethodListTools is the method name for a ListTools operation.
const MethodListTools = ".ListTools"

// MethodCallTool is the method name for a CallTool operation.
const MethodCallTool = ".CallTool"

// ListToolsWorkflowName returns the full workflow name for a ListTools
// operation on the given MCPServer: dapr.internal.mcp.<server>.ListTools
func ListToolsWorkflowName(serverName string) string {
	return WorkflowNamePrefix + serverName + MethodListTools
}

// CallToolWorkflowName returns the full workflow name for a CallTool
// operation on the given MCPServer: dapr.internal.mcp.<server>.CallTool
func CallToolWorkflowName(serverName string) string {
	return WorkflowNamePrefix + serverName + MethodCallTool
}
