/*
Copyright 2022 The Dapr Authors
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
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	"github.com/dapr/components-contrib/conversation"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	kmeta "github.com/dapr/kit/metadata"
	"github.com/tmc/langchaingo/llms"
)

// needsInputScrubber checks if PII scrubbing is needed for any input
func needsInputScrubber(req *runtimev1pb.ConversationRequest) bool {
	for _, input := range req.GetInputs() {
		if input.GetScrubPII() {
			return true
		}
	}
	return false
}

// needsOutputScrubber checks if PII scrubbing is needed for outputs
func needsOutputScrubber(req *runtimev1pb.ConversationRequest) bool {
	return req.GetScrubPII()
}

// convertProtoToolsToLangChain converts protobuf tool definitions to LangChain Go format
func convertProtoToolsToLangChain(protoTools []*runtimev1pb.Tool) []llms.Tool {
	if len(protoTools) == 0 {
		return nil
	}

	tools := make([]llms.Tool, 0, len(protoTools))
	for _, protoTool := range protoTools {
		if protoTool.GetFunction() == nil {
			continue
		}

		// Parse parameters JSON schema
		var parameters map[string]any
		if paramStr := protoTool.GetFunction().GetParameters(); paramStr != "" {
			if err := json.Unmarshal([]byte(paramStr), &parameters); err != nil {
				// Log error but continue with empty parameters
				parameters = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}
		} else {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}

		tool := llms.Tool{
			Type: protoTool.GetType(),
			Function: &llms.FunctionDefinition{
				Name:        protoTool.GetFunction().GetName(),
				Description: protoTool.GetFunction().GetDescription(),
				Parameters:  parameters,
			},
		}
		tools = append(tools, tool)
	}

	return tools
}

// convertLangChainToolCallsToProto converts LangChain Go tool calls to protobuf format
func convertLangChainToolCallsToProto(langchainResponse *llms.ContentResponse) []*runtimev1pb.ToolCall {
	if langchainResponse == nil || len(langchainResponse.Choices) == 0 {
		return nil
	}

	var allToolCalls []*runtimev1pb.ToolCall
	for _, choice := range langchainResponse.Choices {
		for _, toolCall := range choice.ToolCalls {
			protoToolCall := &runtimev1pb.ToolCall{
				Id:   toolCall.ID,
				Type: toolCall.Type,
				Function: &runtimev1pb.ToolCallFunction{
					Name:      toolCall.FunctionCall.Name,
					Arguments: toolCall.FunctionCall.Arguments,
				},
			}
			allToolCalls = append(allToolCalls, protoToolCall)
		}
	}

	return allToolCalls
}

// convertProtoToolsToComponentsContrib converts protobuf tools to components-contrib format
func convertProtoToolsToComponentsContrib(protoTools []*runtimev1pb.Tool) []conversation.Tool {
	if len(protoTools) == 0 {
		return nil
	}

	tools := make([]conversation.Tool, len(protoTools))
	for i, protoTool := range protoTools {
		tool := conversation.Tool{
			Type: protoTool.GetType(),
			Function: conversation.ToolFunction{
				Name:        protoTool.GetFunction().GetName(),
				Description: protoTool.GetFunction().GetDescription(),
				Parameters:  protoTool.GetFunction().GetParameters(), // Keep as string for now
			},
		}
		tools[i] = tool
	}

	return tools
}

// convertComponentsContribToolCallsToProto converts components-contrib tool calls to protobuf format
func convertComponentsContribToolCallsToProto(componentToolCalls []conversation.ToolCall) []*runtimev1pb.ToolCall {
	if len(componentToolCalls) == 0 {
		return nil
	}

	protoToolCalls := make([]*runtimev1pb.ToolCall, len(componentToolCalls))
	for i, componentToolCall := range componentToolCalls {
		protoToolCall := &runtimev1pb.ToolCall{
			Id:   componentToolCall.ID,
			Type: componentToolCall.Type,
			Function: &runtimev1pb.ToolCallFunction{
				Name:      componentToolCall.Function.Name,
				Arguments: componentToolCall.Function.Arguments,
			},
		}
		protoToolCalls[i] = protoToolCall
	}

	return protoToolCalls
}

// extractToolDefinitionsFromInputs extracts tool definitions from the first input message
func extractToolDefinitionsFromInputs(inputs []*runtimev1pb.ConversationInput) []*runtimev1pb.Tool {
	if len(inputs) == 0 {
		return nil
	}

	// Tool definitions should be in the first input message only
	return inputs[0].GetTools()
}

// getInputsFromRequest gets the inputs from the request and scrubs them if PII scrubbing is enabled on the input
func getInputsFromRequest(req *runtimev1pb.ConversationRequest, scrubber piiscrubber.Scrubber, logger logger.Logger) ([]conversation.ConversationInput, error) {
	reqInputs := req.GetInputs()
	if reqInputs == nil {
		return nil, messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	inputs := make([]conversation.ConversationInput, 0, len(reqInputs))
	for _, i := range reqInputs {
		msg := i.GetContent()

		if i.GetScrubPII() && scrubber != nil {
			scrubbed, sErr := scrubber.ScrubTexts([]string{i.GetContent()})
			if sErr != nil {
				sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
				logger.Debug(sErr)
				return nil, sErr
			}

			msg = scrubbed[0]
		}

		// Determine the role based on input type
		role := conversation.Role(i.GetRole())

		// Handle tool result messages
		if i.GetToolCallId() != "" && i.GetName() != "" {
			// This is a tool result message - set role to "tool"
			role = conversation.RoleTool
		}

		c := conversation.ConversationInput{
			Message: msg,
			Role:    role,
		}

		// Add tools if present in this input (typically first input only)
		if len(i.GetTools()) > 0 {
			c.Tools = convertProtoToolsToComponentsContrib(i.GetTools())
		}

		// Add tool call ID and name for tool result messages
		if i.GetToolCallId() != "" {
			c.ToolCallID = i.GetToolCallId()
		}
		if i.GetName() != "" {
			c.Name = i.GetName()
		}

		inputs = append(inputs, c)
	}

	return inputs, nil
}

func (a *Universal) ConverseAlpha1(ctx context.Context, req *runtimev1pb.ConversationRequest) (*runtimev1pb.ConversationResponse, error) {
	// valid component
	if a.compStore.ConversationsLen() == 0 {
		err := messages.ErrConversationNotFound
		a.logger.Debug(err)
		return nil, err
	}

	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	// prepare request
	request := &conversation.ConversationRequest{}
	err := kmeta.DecodeMetadata(req.GetMetadata(), request)
	if err != nil {
		return nil, err
	}

	if len(req.GetInputs()) == 0 {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	// prepare scrubber in case PII scrubbing is enabled (either at request level or input level)
	var scrubber piiscrubber.Scrubber

	// Check if PII scrubbing is needed for inputs or outputs
	needsInputPIIScrubbing := needsInputScrubber(req)
	needsOutputPIIScrubbing := needsOutputScrubber(req)

	if needsInputPIIScrubbing || needsOutputPIIScrubbing {
		scrubber, err = piiscrubber.NewDefaultScrubber()
		if err != nil {
			err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
			a.logger.Debug(err)
			return &runtimev1pb.ConversationResponse{}, err
		}
	}

	inputs, err := getInputsFromRequest(req, scrubber, a.logger)
	if err != nil {
		return &runtimev1pb.ConversationResponse{}, err
	}

	request.Inputs = inputs
	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextID()
	request.Temperature = req.GetTemperature()

	// NEW: Extract tool definitions and convert to LangChain format for components
	protoTools := extractToolDefinitionsFromInputs(req.GetInputs())
	langchainTools := convertProtoToolsToLangChain(protoTools)

	if len(langchainTools) > 0 {
		a.logger.Debugf("Tool calling request with %d tools for component %s", len(langchainTools), req.GetName())
	}

	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.ConversationResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*conversation.ConversationResponse, error) {
		// NEW: Call component with regular interface
		// Components that support tool calling should handle tools from input.Tools
		rResp, rErr := component.Converse(ctx, request)
		return rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)
	usage := convertUsageToProto(resp)
	diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), err == nil, elapsed, diag.NonStreamingConversation, int64(usage.GetPromptTokens()), int64(usage.GetCompletionTokens()))

	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponse{}, err
	}

	// handle response
	response := &runtimev1pb.ConversationResponse{}
	a.logger.Debug(response)
	if resp != nil {
		if resp.ConversationContext != "" {
			response.ContextID = &resp.ConversationContext
		}

		for _, o := range resp.Outputs {
			res := o.Result

			if needsOutputPIIScrubbing {
				scrubbed, sErr := scrubber.ScrubTexts([]string{o.Result})
				if sErr != nil {
					sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
					a.logger.Debug(sErr)
					return &runtimev1pb.ConversationResponse{}, sErr
				}

				res = scrubbed[0]
			}

			// NEW: Create conversation result with tool calling support
			result := &runtimev1pb.ConversationResult{
				Result:     res,
				Parameters: o.Parameters,
			}

			// NEW: Convert tool calls from components-contrib format to proto format
			if len(o.ToolCalls) > 0 {
				result.ToolCalls = convertComponentsContribToolCallsToProto(o.ToolCalls)
			}

			// NEW: Set finish reason if provided
			if o.FinishReason != "" {
				result.FinishReason = func(s string) *string { return &s }(o.FinishReason)
			}

			response.Outputs = append(response.GetOutputs(), result)
		}
		response.Usage = usage
	}

	return response, nil
}
