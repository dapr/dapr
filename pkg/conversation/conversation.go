/*
Copyright 2025 The Dapr Authors
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

package conversation

import (
	piiscrubber "github.com/aavaz-ai/pii-scrubber"
	contribConverse "github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	kmeta "github.com/dapr/kit/metadata"
	"github.com/dapr/kit/ptr"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func CreateComponentRequest(req *runtimev1pb.ConversationRequest, scrubber piiscrubber.Scrubber) (*contribConverse.ConversationRequest, error) {
	reqTools := ConvertProtoToolsToComponentsContrib(req.GetTools())
	componentRequest := &contribConverse.ConversationRequest{
		Context:     req.GetContextID(),
		Parameters:  req.GetParameters(),
		Temperature: req.GetTemperature(),
		Tools:       reqTools,
	}
	err := kmeta.DecodeMetadata(req.GetMetadata(), componentRequest)
	if err != nil {
		return nil, err
	}

	componentRequest.Inputs, err = GetInputsFromRequest(req, scrubber)
	if err != nil {
		return nil, err
	}

	return componentRequest, nil
}

// ConvertUsageToProto converts components-contrib UsageInfo to Dapr protobuf ConversationUsage
func ConvertUsageToProto(resp *contribConverse.ConversationResponse) *runtimev1pb.ConversationUsage {
	if resp == nil || resp.Usage == nil {
		return nil
	}

	return &runtimev1pb.ConversationUsage{
		PromptTokens:     ptr.Of(resp.Usage.PromptTokens),
		CompletionTokens: ptr.Of(resp.Usage.CompletionTokens),
		TotalTokens:      ptr.Of(resp.Usage.TotalTokens),
	}
}

// NeedsInputScrubber checks if PII scrubbing is needed for any input
func NeedsInputScrubber(req *runtimev1pb.ConversationRequest) bool {
	for _, input := range req.GetInputs() {
		if input.GetScrubPII() {
			return true
		}
	}
	return false
}

// NeedsOutputScrubber checks if PII scrubbing is needed for outputs
func NeedsOutputScrubber(req *runtimev1pb.ConversationRequest) bool {
	return req.GetScrubPII()
}

// ConvertProtoToolsToComponentsContrib converts protobuf tools to components-contrib format
func ConvertProtoToolsToComponentsContrib(protoTools []*runtimev1pb.ConversationTool) []contribConverse.Tool {
	if len(protoTools) == 0 {
		return nil
	}

	tools := make([]contribConverse.Tool, len(protoTools))
	for i, protoTool := range protoTools {
		tool := contribConverse.Tool{
			Type: protoTool.GetType().GetValue(),
			Function: contribConverse.ToolFunction{
				Name:        protoTool.GetName().GetValue(),
				Description: protoTool.GetDescription().GetValue(),
				Parameters:  protoTool.GetParameters().GetValue(),
			},
		}
		tools[i] = tool
	}

	return tools
}

// ConvertComponentsContribToolCallsToProto converts components-contrib tool calls to protobuf format
func ConvertComponentsContribToolCallsToProto(componentToolCalls []contribConverse.ToolCall) []*runtimev1pb.ConversationToolCall {
	if len(componentToolCalls) == 0 {
		return nil
	}

	protoToolCalls := make([]*runtimev1pb.ConversationToolCall, len(componentToolCalls))
	for i, componentToolCall := range componentToolCalls {
		protoToolCall := &runtimev1pb.ConversationToolCall{
			Id:        wrapperspb.String(componentToolCall.ID),
			Type:      wrapperspb.String(componentToolCall.CallType),
			Name:      wrapperspb.String(componentToolCall.Function.Name),
			Arguments: wrapperspb.String(componentToolCall.Function.Arguments),
		}
		protoToolCalls[i] = protoToolCall
	}

	return protoToolCalls
}

// convertComponentsContribPartsToProto converts components-contrib content parts to protobuf format
func convertComponentsContribPartsToProto(result string) []*runtimev1pb.ConversationContent {
	// Add text content if present
	if result != "" {
		return []*runtimev1pb.ConversationContent{
			{
				ContentType: &runtimev1pb.ConversationContent_Text{
					Text: &runtimev1pb.ConversationText{
						Text: result,
					},
				},
			},
		}
	}

	return nil
}

// convertProtoPartsToComponentsContrib converts protobuf parts to components-contrib parts
func convertProtoPartsToComponentsContrib(protoParts []*runtimev1pb.ConversationContent) []contribConverse.ConversationContent {
	parts := make([]contribConverse.ConversationContent, 0, len(protoParts))

	for _, part := range protoParts {
		switch content := part.GetContentType().(type) {
		case *runtimev1pb.ConversationContent_Text:
			parts = append(parts, contribConverse.TextContentPart{
				Text: content.Text.GetText(),
			})
		case *runtimev1pb.ConversationContent_ToolCall:
			parts = append(parts, contribConverse.ToolCallRequest{
				ID:       content.ToolCall.GetId().GetValue(),
				CallType: content.ToolCall.GetType().GetValue(),
				Function: contribConverse.ToolCallFunction{
					Name:      content.ToolCall.GetName().GetValue(),
					Arguments: content.ToolCall.GetArguments().GetValue(),
				},
			})
		}
		// TODO(@Sicoyle): add tool result part???
	}

	return parts
}

// convertComponentsContribContentPartsToProto converts components-contrib content parts to protobuf format
func convertComponentsContribContentPartsToProto(parts []contribConverse.ConversationContent, scrubber piiscrubber.Scrubber, componentName string) ([]*runtimev1pb.ConversationContent, error) {
	if len(parts) == 0 {
		return nil, nil
	}

	protoParts := make([]*runtimev1pb.ConversationContent, 0, len(parts))
	for _, part := range parts {
		switch p := part.(type) {
		case contribConverse.TextContentPart:
			if scrubber != nil {
				scrubbed, err := scrubber.ScrubTexts([]string{p.Text})
				if err != nil {
					return nil, messages.ErrConversationInvoke.WithFormat(componentName, err.Error())
				}
				p.Text = scrubbed[0]
			}
			protoParts = append(protoParts, &runtimev1pb.ConversationContent{
				ContentType: &runtimev1pb.ConversationContent_Text{
					Text: &runtimev1pb.ConversationText{
						Text: p.Text,
					},
				},
			})
		case contribConverse.ToolCallRequest:
			protoParts = append(protoParts, &runtimev1pb.ConversationContent{
				ContentType: &runtimev1pb.ConversationContent_ToolCall{
					ToolCall: &runtimev1pb.ConversationToolCall{
						Id:   wrapperspb.String(p.ID),
						Type: wrapperspb.String(p.CallType),
						Name: wrapperspb.String(p.Function.Name),
						// TODO: add another option to scrub arguments from LLM
						Arguments: wrapperspb.String(p.Function.Arguments),
					},
				},
			})
		default:
			// Unknown part type, skip it
			continue
		}
	}

	return protoParts, nil
}

// GetInputsFromRequest gets the inputs from the request and scrubs them if PII scrubbing is enabled on the input
// TODO(@Sicoyle): get with josh on this. I believe we just had a single PII scrubber before? Now we have two,
// but it is the case now that as part of our input, we include ToolCallResponse potentially, so kind of backwards thinking on just an input vs output scrubber.
func GetInputsFromRequest(req *runtimev1pb.ConversationRequest, scrubber piiscrubber.Scrubber) ([]contribConverse.ConversationInput, error) {
	reqInputs := req.GetInputs()
	if reqInputs == nil {
		return nil, messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	inputs := make([]contribConverse.ConversationInput, 0, len(reqInputs))
	for _, i := range reqInputs {
		if i == nil {
			continue
		}
		role := contribConverse.Role(i.GetRole())
		var msg string

		// Process content parts if available
		if len(i.GetContent()) > 0 {
			for _, part := range i.GetContent() {
				if textContent := part.GetText(); textContent != nil {
					msg = textContent.GetText()
				}

				// Apply PII scrubbing to text content
				if i.GetScrubPII() && scrubber != nil && msg != "" {
					scrubbed, err := scrubber.ScrubTexts([]string{msg})
					if err != nil {
						return nil, messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
					}
					msg = scrubbed[0]
				}
			}
		}

		if len(i.GetToolResults()) > 0 {
			role = contribConverse.RoleTool
		}

		// Convert protobuf parts directly to components-contrib content parts
		var parts []contribConverse.ConversationContent
		parts = convertProtoPartsToComponentsContrib(i.GetContent())

		for _, toolResult := range i.GetToolResults() {
			// TODO(@Sicoyle): right here I don't think we should combine a ToolCallResponse into ConversationInput.Content.
			// It should be a separate field.
			parts = append(parts, contribConverse.ToolCallResponse{
				ToolCallID: toolResult.GetId(),
				Name:       toolResult.GetName(),
				Content:    toolResult.GetOutputText(),
				IsError:    toolResult.GetError() != nil,
			})
		}

		// Create conversation input with content parts
		inputs = append(inputs, contribConverse.ConversationInput{
			Role:    role,
			Content: parts,
		})
	}

	return inputs, nil
}

func ConvertComponentsContribOutputToProto(conversationOutputs []contribConverse.ConversationOutput, scrubber piiscrubber.Scrubber, componentName string) ([]*runtimev1pb.ConversationResult, error) {
	if len(conversationOutputs) == 0 {
		return nil, nil
	}
	outputs := make([]*runtimev1pb.ConversationResult, 0, len(conversationOutputs))
	for _, o := range conversationOutputs {
		// Create conversation result with tool calling support and parts
		output := &runtimev1pb.ConversationResult{
			Parameters: o.Parameters,
		}

		if len(o.Content) > 0 {
			var err error
			output.Content, err = convertComponentsContribContentPartsToProto(o.Content, scrubber, componentName)
			if err != nil {
				return nil, err
			}
		}

		finishReason := o.FinishReason
		if finishReason == "" {
			// Set finish reason (hardcoded for now, components don't provide this yet)
			finishReason = "stop"
			// Check if there are tool calls in the parts
			if len(o.Content) > 0 {
				for _, part := range o.Content {
					if _, ok := part.(contribConverse.ToolCallResponse); ok {
						finishReason = "tool_calls"
					}
				}
			}
		}
		output.FinishReason = ptr.Of(finishReason)

		outputs = append(outputs, output)
	}

	return outputs, nil
}
