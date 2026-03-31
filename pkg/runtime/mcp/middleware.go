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
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	"github.com/dapr/durabletask-go/task"
)

// runBeforeCall schedules the beforeCall workflow as a child orchestration and
// awaits it. Returns nil if no middleware is configured or if the workflow
// succeeds. Returns an error if the workflow fails (call should be aborted).
func runBeforeCall(
	ctx *task.OrchestrationContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
) error {
	if server.Spec.Middleware == nil || server.Spec.Middleware.BeforeCall == nil {
		return nil
	}
	input := BeforeCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments}
	t := ctx.CallSubOrchestrator(*server.Spec.Middleware.BeforeCall,
		task.WithSubOrchestratorInput(input))
	return t.Await(nil)
}

// runAfterCall schedules the afterCall workflow as a fire-and-forget child
// orchestration. Errors scheduling the sub-orchestration are logged; they
// never affect the result returned to the caller.
func runAfterCall(
	ctx *task.OrchestrationContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
	result any,
) {
	if server.Spec.Middleware == nil || server.Spec.Middleware.AfterCall == nil {
		return
	}
	input := AfterCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments, Result: result}
	// Fire-and-forget: do not await the task.
	ctx.CallSubOrchestrator(*server.Spec.Middleware.AfterCall,
		task.WithSubOrchestratorInput(input))
}
