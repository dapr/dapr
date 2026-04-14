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

// runBeforeCallTool executes the beforeCallTool middleware pipeline in order.
// If any hook returns an error, the chain stops and the error is returned.
func runBeforeCallTool(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
) error {
	if server.Spec.Middleware == nil {
		return nil
	}
	input := BeforeCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments}
	for _, hook := range server.Spec.Middleware.BeforeCallTool {
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

// runAfterCallTool executes the afterCallTool middleware pipeline in order.
// Each hook is awaited so errors can be logged, but failures do not affect
// the result returned to the caller.
func runAfterCallTool(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments map[string]any,
	result any,
) {
	if server.Spec.Middleware == nil {
		return
	}
	input := AfterCallInput{MCPServerName: serverName, ToolName: tool, Arguments: arguments, Result: result}
	for _, hook := range server.Spec.Middleware.AfterCallTool {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if err := t.Await(nil); err != nil {
			workerLog.Warnf("afterCallTool hook %q failed for tool %q on MCPServer %q: %s",
				hook.Workflow.WorkflowName, tool, serverName, err)
		}
	}
}

// runBeforeListTools executes the beforeListTools middleware pipeline in order.
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
// See runAfterCallTool for semantics — errors are logged but don't affect
// the result.
func runAfterListTools(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName string,
	result any,
) {
	if server.Spec.Middleware == nil {
		return
	}
	input := AfterCallInput{MCPServerName: serverName, Result: result}
	for _, hook := range server.Spec.Middleware.AfterListTools {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			task.WithChildWorkflowInput(input))
		if err := t.Await(nil); err != nil {
			workerLog.Warnf("afterListTools hook %q failed for MCPServer %q: %s",
				hook.Workflow.WorkflowName, serverName, err)
		}
	}
}
