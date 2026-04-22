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

// Package types provides workflow name constants and helpers for the MCP in-process workflow subsystem.
package types

// WorkflowNamePrefix is the prefix shared by all built-in MCP orchestrations.
// Format: dapr.internal.mcp.<server>.<method>
const WorkflowNamePrefix = "dapr.internal.mcp."

// MethodListTools is the method suffix for a ListTools operation.
const MethodListTools = ".ListTools"

// MethodCallTool is the method suffix for a CallTool operation.
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

// ListToolsActivityName returns the activity name for a ListTools transport
// call on the given MCPServer: dapr.internal.mcp.<server>.list-tools
func ListToolsActivityName(serverName string) string {
	return WorkflowNamePrefix + serverName + ".list-tools"
}

// CallToolActivityName returns the activity name for a CallTool transport
// call on the given MCPServer: dapr.internal.mcp.<server>.call-tool
func CallToolActivityName(serverName string) string {
	return WorkflowNamePrefix + serverName + ".call-tool"
}
