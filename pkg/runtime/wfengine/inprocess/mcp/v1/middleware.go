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
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/dapr/durabletask-go/task"

	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	wfv1 "github.com/dapr/dapr/pkg/proto/workflows/v1"
)

// hookChildWorkflowOpts builds the CallChildWorkflow options for a middleware hook.
// When the hook's AppID is set, the child workflow targets the remote app via service invocation.
func hookChildWorkflowOpts(wf *mcpserverapi.MCPMiddlewareWorkflow, input any) []task.ChildWorkflowOption {
	opts := []task.ChildWorkflowOption{task.WithChildWorkflowInput(input)}
	if wf.AppID != nil && *wf.AppID != "" {
		opts = append(opts, task.WithChildWorkflowAppID(*wf.AppID))
	}
	return opts
}

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
	arguments *structpb.Struct,
) (*structpb.Struct, error) {
	if server.Spec.Middleware == nil {
		return arguments, nil
	}
	input := &wfv1.MCPBeforeCallToolHookInput{
		Name: serverName, ToolName: tool, Arguments: arguments,
	}
	for _, hook := range server.Spec.Middleware.BeforeCallTool {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			hookChildWorkflowOpts(hook.Workflow, input)...)
		if hook.Mutate != nil && *hook.Mutate {
			var mutated wfv1.MCPBeforeCallToolHookInput
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
// Hook errors fail the workflow — after-hooks may be authz gates.
func runAfterCallTool(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName, tool string,
	arguments *structpb.Struct,
	result *wfv1.CallMCPToolResponse,
) (*wfv1.CallMCPToolResponse, error) {
	if server.Spec.Middleware == nil {
		return result, nil
	}
	input := &wfv1.MCPAfterCallToolHookInput{
		Name: serverName, ToolName: tool, Arguments: arguments, Result: result,
	}
	for _, hook := range server.Spec.Middleware.AfterCallTool {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			hookChildWorkflowOpts(hook.Workflow, input)...)
		if hook.Mutate != nil && *hook.Mutate {
			var mutated wfv1.CallMCPToolResponse
			if err := t.Await(&mutated); err != nil {
				return nil, fmt.Errorf("afterCallTool mutating hook %q failed for tool %q on MCPServer %q: %w",
					hook.Workflow.WorkflowName, tool, serverName, err)
			}
			result = &mutated
			input.Result = result
		} else {
			if err := t.Await(nil); err != nil {
				return nil, fmt.Errorf("afterCallTool hook %q failed for tool %q on MCPServer %q: %w",
					hook.Workflow.WorkflowName, tool, serverName, err)
			}
		}
	}
	return result, nil
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
	input := &wfv1.MCPBeforeListToolsHookInput{Name: serverName}
	for _, hook := range server.Spec.Middleware.BeforeListTools {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			hookChildWorkflowOpts(hook.Workflow, input)...)
		if err := t.Await(nil); err != nil {
			return err
		}
	}
	return nil
}

// runAfterListTools executes the afterListTools middleware pipeline in order.
// See runAfterCallTool for Mutate semantics.
// Hook errors fail the workflow — after-hooks may be authz gates.
func runAfterListTools(
	ctx *task.WorkflowContext,
	server *mcpserverapi.MCPServer,
	serverName string,
	result *wfv1.ListMCPToolsResponse,
) (*wfv1.ListMCPToolsResponse, error) {
	if server.Spec.Middleware == nil {
		return result, nil
	}
	input := &wfv1.MCPAfterListToolsHookInput{Name: serverName, Result: result}
	for _, hook := range server.Spec.Middleware.AfterListTools {
		if hook.Workflow == nil {
			continue
		}
		t := ctx.CallChildWorkflow(hook.Workflow.WorkflowName,
			hookChildWorkflowOpts(hook.Workflow, input)...)
		if hook.Mutate != nil && *hook.Mutate {
			var mutated wfv1.ListMCPToolsResponse
			if err := t.Await(&mutated); err != nil {
				return nil, fmt.Errorf("afterListTools mutating hook %q failed for MCPServer %q: %w",
					hook.Workflow.WorkflowName, serverName, err)
			}
			result = &mutated
			input.Result = result
		} else {
			if err := t.Await(nil); err != nil {
				return nil, fmt.Errorf("afterListTools hook %q failed for MCPServer %q: %w",
					hook.Workflow.WorkflowName, serverName, err)
			}
		}
	}
	return result, nil
}
