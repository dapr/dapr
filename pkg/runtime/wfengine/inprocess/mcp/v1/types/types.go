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

// CallToolWorkflowInput is the input deserialized by the
// dapr.internal.mcp.<server>.CallTool.<tool> workflow.
type CallToolWorkflowInput struct {
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolActivityInput is the internal input passed from the orchestrator
// to the CallTool activity.
type CallToolActivityInput struct {
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}
