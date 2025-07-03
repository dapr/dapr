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
	"github.com/dapr/kit/ptr"
)

// ConvertUsageToProto converts components-contrib UsageInfo to Dapr protobuf ConversationUsage
func ConvertUsageToProto(resp *contribConverse.ConversationResponse) *runtimev1pb.ConversationUsage {
	if resp == nil || resp.Usage == nil {
		return nil
	}

	// Safely convert uint64 to uint32 for token counts
	// Token counts realistically never exceed uint32 range (~4.3 billion)
	return &runtimev1pb.ConversationUsage{
		PromptTokens:     ptr.Of(uint32(resp.Usage.PromptTokens)),
		CompletionTokens: ptr.Of(uint32(resp.Usage.CompletionTokens)),
		TotalTokens:      ptr.Of(uint32(resp.Usage.TotalTokens)),
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
func ConvertProtoToolsToComponentsContrib(protoTools []*runtimev1pb.Tool) []contribConverse.Tool {
	if len(protoTools) == 0 {
		return nil
	}

	tools := make([]contribConverse.Tool, len(protoTools))
	for i, protoTool := range protoTools {
		tool := contribConverse.Tool{
			ToolType: protoTool.GetType(),
			Function: contribConverse.ToolFunction{
				Name:        protoTool.GetName(),
				Description: protoTool.GetDescription(),
				Parameters:  protoTool.GetParameters(), // Keep as string for now
			},
		}
		tools[i] = tool
	}

	return tools
}

// ConvertComponentsContribToolCallsToProto converts components-contrib tool calls to protobuf format
func ConvertComponentsContribToolCallsToProto(componentToolCalls []contribConverse.ToolCall) []*runtimev1pb.ToolCall {
	if len(componentToolCalls) == 0 {
		return nil
	}

	protoToolCalls := make([]*runtimev1pb.ToolCall, len(componentToolCalls))
	for i, componentToolCall := range componentToolCalls {
		protoToolCall := &runtimev1pb.ToolCall{
			Id:        componentToolCall.ID,
			Type:      componentToolCall.CallType,
			Name:      componentToolCall.Function.Name,
			Arguments: componentToolCall.Function.Arguments,
		}
		protoToolCalls[i] = protoToolCall
	}

	return protoToolCalls
}

// extractToolCallIDFromParts extracts tool call ID from tool result parts
func extractToolCallIDFromParts(parts []*runtimev1pb.ContentPart) string {
	for _, part := range parts {
		if toolResult := part.GetToolResult(); toolResult != nil {
			return toolResult.GetToolCallId()
		}
	}
	return ""
}

// extractToolNameFromParts extracts tool name from tool result parts
func extractToolNameFromParts(parts []*runtimev1pb.ContentPart) string {
	for _, part := range parts {
		if toolResult := part.GetToolResult(); toolResult != nil {
			return toolResult.GetName()
		}
	}
	return ""
}

// convertComponentsContribPartsToProto converts components-contrib content parts to protobuf format
func convertComponentsContribPartsToProto(result string) []*runtimev1pb.ContentPart {
	// Add text content if present
	if result != "" {
		return []*runtimev1pb.ContentPart{
			{
				ContentType: &runtimev1pb.ContentPart_Text{
					Text: &runtimev1pb.TextContent{
						Text: result,
					},
				},
			},
		}
	}

	return nil
}

// convertProtoPartsToComponentsContrib converts protobuf content parts to components-contrib format
func convertProtoPartsToComponentsContrib(protoParts []*runtimev1pb.ContentPart) []contribConverse.ContentPart {
	if len(protoParts) == 0 {
		return nil
	}

	parts := make([]contribConverse.ContentPart, 0, len(protoParts))
	for _, protoPart := range protoParts {
		switch content := protoPart.GetContentType().(type) {
		case *runtimev1pb.ContentPart_Text:
			parts = append(parts, contribConverse.TextContentPart{
				Text: content.Text.GetText(),
			})
		case *runtimev1pb.ContentPart_ToolCall:
			parts = append(parts, contribConverse.ToolCallContentPart{
				ID:       content.ToolCall.GetId(),
				CallType: content.ToolCall.GetType(),
				Function: contribConverse.ToolCallFunction{
					Name:      content.ToolCall.GetName(),
					Arguments: content.ToolCall.GetArguments(),
				},
			})
		case *runtimev1pb.ContentPart_ToolResult:
			parts = append(parts, contribConverse.ToolResultContentPart{
				ToolCallID: content.ToolResult.GetToolCallId(),
				Name:       content.ToolResult.GetName(),
				Content:    content.ToolResult.GetContent(),
				IsError:    content.ToolResult.GetIsError(),
			})
			// Removed ToolDefinitionsContent handling - tools are now at request level
		}
	}

	return parts
}

// convertComponentsContribContentPartsToProto converts components-contrib content parts to protobuf format
func convertComponentsContribContentPartsToProto(parts []contribConverse.ContentPart, scrubber piiscrubber.Scrubber, componentName string) ([]*runtimev1pb.ContentPart, error) {
	if len(parts) == 0 {
		return nil, nil
	}

	protoParts := make([]*runtimev1pb.ContentPart, 0, len(parts))
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
			protoParts = append(protoParts, &runtimev1pb.ContentPart{
				ContentType: &runtimev1pb.ContentPart_Text{
					Text: &runtimev1pb.TextContent{
						Text: p.Text,
					},
				},
			})
		case contribConverse.ToolCallContentPart:
			protoParts = append(protoParts, &runtimev1pb.ContentPart{
				ContentType: &runtimev1pb.ContentPart_ToolCall{
					ToolCall: &runtimev1pb.ToolCallContent{
						Id:   p.ID,
						Type: p.CallType,
						Name: p.Function.Name,
						// TODO: add another option to scrub arguments from LLM
						Arguments: p.Function.Arguments,
					},
				},
			})
		case contribConverse.ToolResultContentPart:
			protoParts = append(protoParts, &runtimev1pb.ContentPart{
				ContentType: &runtimev1pb.ContentPart_ToolResult{
					// TODO: add another option to scrub arguments to LLM
					ToolResult: &runtimev1pb.ToolResultContent{
						ToolCallId: p.ToolCallID,
						Name:       p.Name,
						Content:    p.Content,
						IsError:    ptr.Of(p.IsError),
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
func GetInputsFromRequest(req *runtimev1pb.ConversationRequest, scrubber piiscrubber.Scrubber) ([]contribConverse.ConversationInput, error) {
	reqInputs := req.GetInputs()
	if reqInputs == nil {
		return nil, messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	inputs := make([]contribConverse.ConversationInput, 0, len(reqInputs))
	for _, i := range reqInputs {
		role := contribConverse.Role(i.GetRole())
		var msg string

		// Process content parts if available
		if len(i.GetParts()) > 0 {
			for _, part := range i.GetParts() {
				// Extract text content from parts
				msg = part.GetText().GetText()

				// Extract tool call ID and name from parts for role detection
				toolCallID := extractToolCallIDFromParts([]*runtimev1pb.ContentPart{part})
				toolName := extractToolNameFromParts([]*runtimev1pb.ContentPart{part})

				// Handle tool result messages - determine role based on tool call ID and name
				if toolCallID != "" && toolName != "" && (role == contribConverse.RoleUser || role == "") {
					role = contribConverse.RoleTool
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
		} else {
			// Fallback to legacy content processing (BACKWARD COMPATIBILITY)
			msg = i.GetContent() //nolint:staticcheck // Intentional use of deprecated field for backward compatibility

			if i.GetScrubPII() && scrubber != nil {
				scrubbed, err := scrubber.ScrubTexts([]string{i.GetContent()}) //nolint:staticcheck // Intentional use of deprecated field for backward compatibility
				if err != nil {
					return nil, messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
				}
				msg = scrubbed[0]
			}
		}

		// Convert protobuf parts directly to components-contrib content parts
		var parts []contribConverse.ContentPart

		if len(i.GetParts()) > 0 {
			// Convert protobuf parts to components-contrib parts directly
			parts = convertProtoPartsToComponentsContrib(i.GetParts())
		} else if msg != "" {
			// Legacy mode: convert string content to text part
			parts = append(parts, contribConverse.TextContentPart{Text: msg})
		}

		// Create conversation input with content parts
		inputs = append(
			inputs,
			contribConverse.ConversationInput{
				Message: msg, // Keep for backward compatibility. TODO: remove this field
				Role:    role,
				Parts:   parts,
			})
	}

	return inputs, nil
}

func ConvertComponentsContribOutputToProto(conversationOutputs []contribConverse.ConversationOutput, scrubber piiscrubber.Scrubber, componentName string) ([]*runtimev1pb.ConversationResult, error) {
	if len(conversationOutputs) == 0 {
		return nil, nil // No outputs to convert
	}
	outputs := make([]*runtimev1pb.ConversationResult, 0, len(conversationOutputs))
	for _, o := range conversationOutputs {
		// TODO: remove on next api version
		res := o.Result //nolint:staticcheck // Intentional use of deprecated field for backward compatibility
		if res != "" && scrubber != nil {
			scrubbed, err := scrubber.ScrubTexts([]string{res})
			if err != nil {
				return nil, messages.ErrConversationInvoke.WithFormat(componentName, err.Error())
			}
			res = scrubbed[0]
		}

		// Create conversation result with tool calling support and parts
		output := &runtimev1pb.ConversationResult{
			Result:     res, // Legacy field for backward compatibility
			Parameters: o.Parameters,
		}

		// Tool calls are now handled through the parts system only

		// Generate parts for rich content support
		if len(o.Parts) > 0 {
			// Convert components-contrib parts to protobuf parts
			var err error
			output.Parts, err = convertComponentsContribContentPartsToProto(o.Parts, scrubber, componentName)
			if err != nil {
				return nil, err
			}
		} else {
			// Fallback to legacy conversion
			output.Parts = convertComponentsContribPartsToProto(res)
		}

		finishReason := o.FinishReason
		if finishReason == "" {
			// Set finish reason (hardcoded for now, components don't provide this yet)
			finishReason = "stop"
			// Check if there are tool calls in the parts
			if len(o.Parts) > 0 {
				for _, part := range o.Parts {
					if _, ok := part.(contribConverse.ToolCallContentPart); ok {
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
