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

import "encoding/json"

// ListToolsInput is the input payload for a dapr.mcp.<name>.ListTools orchestration.
type ListToolsInput struct {
	// MCPServerName is the name of the MCPServer resource.
	MCPServerName string `json:"mcpServerName"`
}

// ListToolsResult is the output of a dapr.mcp.<name>.ListTools orchestration.
type ListToolsResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolDefinition describes a single tool advertised by the MCP server.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// InputSchema is the raw JSON Schema for the tool's input arguments.
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// CallToolInput is the input payload for a dapr.mcp.<name>.CallTool orchestration.
type CallToolInput struct {
	// MCPServerName is the name of the MCPServer resource.
	MCPServerName string `json:"mcpServerName"`
	// ToolName is the name of the MCP tool to invoke.
	ToolName string `json:"toolName"`
	// Arguments are the tool arguments, encoded as a JSON object.
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult is the output of a dapr.mcp.<name>.CallTool orchestration.
// It mirrors the MCP protocol's CallToolResult schema.
// ref: https://modelcontextprotocol.io/specification/2025-11-25/server/tools
type CallToolResult struct {
	// IsError is true when the tool call failed at the MCP level (e.g. auth error,
	// tool not found, transport failure). This is NOT a workflow execution failure.
	IsError bool `json:"isError"`
	// Content holds the content items returned by the tool.
	Content []ContentItem `json:"content,omitempty"`
}

// ContentItem is a single piece of content returned by a tool call.
// Mirrors the MCP spec's unstructured content types:
// text, image, audio, resource_link, and resource (embedded).
// ref: https://modelcontextprotocol.io/specification/2025-11-25/server/tools#tool-result
type ContentItem struct {
	// Type is the content type: "text", "image", "audio", "resource_link", or "resource".
	Type string `json:"type"`
	// Text is set when Type is "text".
	Text string `json:"text,omitempty"`
	// Data is set when Type is "image" or "audio" (base64-encoded).
	Data string `json:"data,omitempty"`
	// MimeType is the MIME type for image, audio, and resource content.
	MimeType string `json:"mimeType,omitempty"`
	// Resource is set when Type is "resource_link" or "resource".
	// It contains the full structured content as raw JSON to preserve all
	// fields without coupling to a specific schema version.
	Resource json.RawMessage `json:"resource,omitempty"`
}

// BeforeCallInput is passed to the beforeCall workflow.
// ToolName is empty for ListTools orchestrations.
type BeforeCallInput struct {
	MCPServerName string         `json:"mcpServerName"`
	ToolName      string         `json:"toolName,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
}

// AfterCallInput is passed to the afterCall workflow.
// ToolName is empty for ListTools orchestrations.
type AfterCallInput struct {
	MCPServerName string         `json:"mcpServerName"`
	ToolName      string         `json:"toolName,omitempty"`
	Arguments     map[string]any `json:"arguments,omitempty"`
	Result        any            `json:"result"`
}

// orchestrationNamePrefix is the prefix shared by all built-in MCP orchestrations.
// All dapr-internal workflows live under dapr.internal.*; MCP claims the mcp sub-namespace.
const orchestrationNamePrefix = "dapr.internal.mcp."

// suffixListTools is the suffix identifying a ListTools orchestration.
const suffixListTools = ".ListTools"

// suffixCallTool is the suffix identifying a CallTool orchestration.
const suffixCallTool = ".CallTool"

// ListToolsWorkflowName returns the full workflow name for a ListTools
// operation on the given MCPServer.
func ListToolsWorkflowName(serverName string) string {
	return orchestrationNamePrefix + serverName + suffixListTools
}

// CallToolWorkflowName returns the full workflow name for a CallTool
// operation on the given MCPServer.
func CallToolWorkflowName(serverName string) string {
	return orchestrationNamePrefix + serverName + suffixCallTool
}
