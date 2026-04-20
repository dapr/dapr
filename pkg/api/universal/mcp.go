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

package universal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dapr/components-contrib/workflows"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	mcptypes "github.com/dapr/dapr/pkg/runtime/wfengine/inprocess/mcp/types"
	"github.com/dapr/durabletask-go/api/helpers"
	"github.com/dapr/durabletask-go/api/protos"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	mcpWorkflowPollInterval = 100 * time.Millisecond
	mcpWorkflowPollTimeout  = 60 * time.Second
)

// ListMCPToolsAlpha1 lists the tools available on a declared MCPServer resource.
// Internally, the call is routed through the workflow engine (dapr.internal.mcp.<server>.ListTools).
func (a *Universal) ListMCPToolsAlpha1(ctx context.Context, in *runtimev1pb.ListMCPToolsRequest) (*runtimev1pb.ListMCPToolsResponse, error) {
	if in.GetMcpServerName() == "" {
		return nil, fmt.Errorf("mcp_server_name is required")
	}

	input, err := json.Marshal(mcptypes.ListToolsInput{
		MCPServerName: in.GetMcpServerName(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ListTools input: %w", err)
	}

	output, err := a.startAndWaitMCPWorkflow(ctx,
		mcptypes.ListToolsWorkflowName(in.GetMcpServerName()),
		string(input),
	)
	if err != nil {
		return nil, fmt.Errorf("ListMCPTools failed: %w", err)
	}

	var result mcptypes.ListToolsResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ListTools result: %w", err)
	}

	resp := &runtimev1pb.ListMCPToolsResponse{
		Tools: make([]*runtimev1pb.MCPToolDefinition, 0, len(result.Tools)),
	}
	for _, t := range result.Tools {
		td := &runtimev1pb.MCPToolDefinition{
			Name:        t.Name,
			Description: t.Description,
		}
		if len(t.InputSchema) > 0 {
			td.InputSchema = t.InputSchema
		}
		resp.Tools = append(resp.Tools, td)
	}
	return resp, nil
}

// CallMCPToolAlpha1 invokes a tool on a declared MCPServer resource.
// Internally, the call is routed through the workflow engine (dapr.internal.mcp.<server>.CallTool).
func (a *Universal) CallMCPToolAlpha1(ctx context.Context, in *runtimev1pb.CallMCPToolRequest) (*runtimev1pb.CallMCPToolResponse, error) {
	if in.GetMcpServerName() == "" {
		return nil, fmt.Errorf("mcp_server_name is required")
	}
	if in.GetToolName() == "" {
		return nil, fmt.Errorf("tool_name is required")
	}

	var args map[string]any
	if len(in.GetArguments()) > 0 {
		if err := json.Unmarshal(in.GetArguments(), &args); err != nil {
			return nil, fmt.Errorf("invalid arguments JSON: %w", err)
		}
	}

	callInput := mcptypes.CallToolInput{
		MCPServerName: in.GetMcpServerName(),
		ToolName:      in.GetToolName(),
		Arguments:     args,
	}
	input, err := json.Marshal(callInput)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CallTool input: %w", err)
	}

	output, err := a.startAndWaitMCPWorkflow(ctx,
		mcptypes.CallToolWorkflowName(in.GetMcpServerName()),
		string(input),
	)
	if err != nil {
		return nil, fmt.Errorf("CallMCPTool failed: %w", err)
	}

	var result mcptypes.CallToolResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal CallTool result: %w", err)
	}

	resp := &runtimev1pb.CallMCPToolResponse{
		IsError: result.IsError,
		Content: make([]*runtimev1pb.MCPContentItem, 0, len(result.Content)),
	}
	for _, c := range result.Content {
		resp.Content = append(resp.Content, &runtimev1pb.MCPContentItem{
			Type:     c.Type,
			Text:     c.Text,
			Data:     c.Data,
			MimeType: c.MimeType,
			Resource: c.Resource,
		})
	}
	return resp, nil
}

// startAndWaitMCPWorkflow starts an MCP workflow and polls for completion.
// Returns the workflow output (JSON string) or an error.
func (a *Universal) startAndWaitMCPWorkflow(ctx context.Context, workflowName, inputJSON string) (string, error) {
	if a.workflowEngine == nil {
		return "", fmt.Errorf("workflow engine is not available")
	}

	startResp, err := a.workflowEngine.Client().Start(ctx, &workflows.StartRequest{
		WorkflowName:  workflowName,
		WorkflowInput: wrapperspb.String(inputJSON),
	})
	if err != nil {
		return "", fmt.Errorf("failed to start workflow %q: %w", workflowName, err)
	}

	instanceID := startResp.InstanceID

	deadline := time.After(mcpWorkflowPollTimeout)
	ticker := time.NewTicker(mcpWorkflowPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("workflow %q (%s) did not complete within %s", workflowName, instanceID, mcpWorkflowPollTimeout)
		case <-ticker.C:
			getResp, err := a.workflowEngine.Client().Get(ctx, &workflows.GetRequest{
				InstanceID: instanceID,
			})
			if err != nil {
				return "", fmt.Errorf("failed to get workflow status: %w", err)
			}

			switch getResp.Workflow.RuntimeStatus {
			case helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED):
				output := getResp.Workflow.Properties["dapr.workflow.output"]
				return output, nil
			case helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED):
				failureMsg := getResp.Workflow.Properties["dapr.workflow.failure"]
				return "", fmt.Errorf("workflow failed: %s", failureMsg)
			case helpers.ToRuntimeStatusString(protos.OrchestrationStatus_ORCHESTRATION_STATUS_TERMINATED):
				return "", fmt.Errorf("workflow was terminated")
			default:
				// Still running — continue polling.
			}
		}
	}
}
