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
	. "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/types"
	"github.com/dapr/durabletask-go/task"
)

// runBeforeCallTool executes the beforeCallTool middleware pipeline in order.
// If any hook returns an error, the chain stops and the error is returned.
// When a hook has Mutate=true, its return value replaces the arguments flowing to subsequent hooks,
// and ultimately to the tool call (e.g. redacting PII, injecting defaults).
// When Mutate=false (default), the hook validates/gates only — its output is discarded.
// Returns the (potentially mutated) arguments to use for the tool call.
func runBeforeCallTool(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
) (map[string]any, error) {
	if server.Spec.Middleware == nil {
		return arguments, nil
	}
	input := BeforeCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments}
	for _, hook := range server.Spec.Middleware.BeforeCallTool {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if hook.Mutate {
			var mutated BeforeCallInput
			if err := t.Await(&mutated); err != nil {
				return nil, err
			}
			arguments = mutated.Arguments
			// Update input for the next hook in the chain so it sees the mutated arguments.
			input.Arguments = arguments
		} else {
			if err := t.Await(nil); err != nil {
				return nil, err
			}
		}
	}
	return arguments, nil
}

// runAfterCallTool executes the afterCallTool middleware pipeline in order.
// When a hook has Mutate=true, its return value replaces the result flowing to the caller.
// When Mutate=false (default), the hook observes but its output is discarded.
// Errors are logged but do not affect the result.
func runAfterCallTool(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
	result *CallToolResult,
) *CallToolResult {
	if server.Spec.Middleware == nil {
		return result
	}
	input := AfterCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments, Result: result}
	for _, hook := range server.Spec.Middleware.AfterCallTool {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if hook.Mutate {
			var mutated CallToolResult
			if err := t.Await(&mutated); err != nil {
				workerLog.Warnf("afterCallTool mutating hook %q failed for tool %q on MCPServer %q: %s",
					hook.Workflow.WorkflowName, tool, serverName, err)
			} else {
				result = &mutated
				// Update input for the next hook in the chain so it sees the mutated result.
				input.Result = result
			}
		} else {
			if err := t.Await(nil); err != nil {
				workerLog.Warnf("afterCallTool hook %q failed for tool %q on MCPServer %q: %s",
					hook.Workflow.WorkflowName, tool, serverName, err)
			}
		}
	}
	return result
}

// runBeforeListTools executes the beforeListTools middleware pipeline in order.
// beforeListTools uses MCPMiddlewareHook (no Mutate field) because ListTools has no input arguments to transform.
func runBeforeListTools(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName string,
) error {
	if server.Spec.Middleware == nil {
		return nil
	}
	input := BeforeCallInput{MCPServerName: serverName}
	for _, hook := range server.Spec.Middleware.BeforeListTools {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if err := t.Await(nil); err != nil {
			return err
		}
	}
	return nil
}

// runAfterListTools executes the afterListTools middleware pipeline in order.
// See runAfterCallTool for Mutate semantics.
func runAfterListTools(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName string,
	result *ListToolsResult,
) *ListToolsResult {
	if server.Spec.Middleware == nil {
		return result
	}
	input := AfterCallInput{MCPServerName: serverName, Result: result}
	for _, hook := range server.Spec.Middleware.AfterListTools {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if hook.Mutate {
			var mutated ListToolsResult
			if err := t.Await(&mutated); err != nil {
				workerLog.Warnf("afterListTools mutating hook %q failed for MCPServer %q: %s",
					hook.Workflow.WorkflowName, serverName, err)
			} else {
				result = &mutated
				input.Result = result
			}
		} else {
			if err := t.Await(nil); err != nil {
				workerLog.Warnf("afterListTools hook %q failed for MCPServer %q: %s",
					hook.Workflow.WorkflowName, serverName, err)
			}
		}
	}
	return result
}
