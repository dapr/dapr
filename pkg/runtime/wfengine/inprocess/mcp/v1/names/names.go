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

package names

// MCP workflow name constants and helpers. These define the canonical naming
// convention for all MCP internal workflows and activities.

const (
	// MCPWorkflowPrefix is the prefix for all MCP internal workflows.
	MCPWorkflowPrefix = "dapr.internal.mcp." //nolint:gosec // workflow name prefix, not a credential

	// MCPMethodListTools is the segment identifier for ListTools operations.
	MCPMethodListTools = "ListTools"

	// MCPMethodCallTool is the segment identifier for CallTool operations.
	MCPMethodCallTool = "CallTool"
)

// MCPListToolsWorkflowName returns the full workflow name for a ListTools
// operation on the given MCPServer: dapr.internal.mcp.<server>.ListTools
func MCPListToolsWorkflowName(serverName string) string {
	return MCPWorkflowPrefix + serverName + "." + MCPMethodListTools
}

// MCPCallToolWorkflowName returns the full workflow name for a CallTool
// operation on the given MCPServer and tool: dapr.internal.mcp.<server>.CallTool.<toolName>
func MCPCallToolWorkflowName(serverName, toolName string) string {
	return MCPWorkflowPrefix + serverName + "." + MCPMethodCallTool + "." + toolName
}

// MCPListToolsActivityName returns the activity name for a ListTools transport
// call on the given MCPServer: dapr.internal.mcp.<server>.list-tools
func MCPListToolsActivityName(serverName string) string {
	return MCPWorkflowPrefix + serverName + ".list-tools"
}

// MCPCallToolActivityName returns the activity name for a CallTool transport
// call on the given MCPServer: dapr.internal.mcp.<server>.call-tool
func MCPCallToolActivityName(serverName string) string {
	return MCPWorkflowPrefix + serverName + ".call-tool"
}
