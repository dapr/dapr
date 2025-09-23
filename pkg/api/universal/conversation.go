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
	"errors"
	"time"

	"github.com/tmc/langchaingo/llms"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/conversation/mistral"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	kmeta "github.com/dapr/kit/metadata"
)

func mapRoleToChatMessageType(roleStr string) llms.ChatMessageType {
	switch roleStr {
	case "user", "human":
		return llms.ChatMessageTypeHuman
	case "system":
		return llms.ChatMessageTypeSystem
	case "assistant", "ai":
		return llms.ChatMessageTypeAI
	case "tool":
		return llms.ChatMessageTypeTool
	case "function":
		return llms.ChatMessageTypeFunction
	case "generic":
		return llms.ChatMessageTypeGeneric
	default:
		return llms.ChatMessageTypeHuman
	}
}

func (a *Universal) ConverseAlpha1(ctx context.Context, req *runtimev1pb.ConversationRequest) (*runtimev1pb.ConversationResponse, error) { //nolint:staticcheck
	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	// prepare request
	request := &conversation.Request{}
	err := kmeta.DecodeMetadata(req.GetMetadata(), request)
	if err != nil {
		return nil, err
	}

	if len(req.GetInputs()) == 0 {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	var scrubber piiscrubber.Scrubber
	scrubber, err = piiscrubber.NewDefaultScrubber()
	if err != nil {
		err = messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponse{}, err //nolint:staticcheck
	}

	var scrubbed []string
	for _, i := range req.GetInputs() {
		msg := i.GetContent()
		if i.GetScrubPII() {
			scrubbed, err = scrubber.ScrubTexts([]string{i.GetContent()})
			if err != nil {
				err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
				a.logger.Debug(err)
				return &runtimev1pb.ConversationResponse{}, err //nolint:staticcheck
			}

			msg = scrubbed[0]
		}

		c := llms.MessageContent{
			Role: mapRoleToChatMessageType(i.GetRole()),
			Parts: []llms.ContentPart{
				llms.TextContent{
					Text: msg,
				},
			},
		}

		if request.Message == nil {
			request.Message = &[]llms.MessageContent{}
		}
		*request.Message = append(*request.Message, c)
	}

	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextID()
	request.Temperature = req.GetTemperature()

	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.Response](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	// transform v1 proto -> v2 component request
	toolsv2 := []llms.Tool{}
	requestv2 := &conversation.Request{
		Message:             request.Message,
		Tools:               &toolsv2,
		Parameters:          request.Parameters,
		ConversationContext: request.ConversationContext,
		Temperature:         request.Temperature,
	}

	resp, err := policyRunner(func(ctx context.Context) (*conversation.Response, error) {
		rResp, rErr := component.Converse(ctx, requestv2)
		return rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), err == nil, elapsed)

	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponse{}, err //nolint:staticcheck
	}

	// handle response
	response := &runtimev1pb.ConversationResponse{} //nolint:staticcheck
	a.logger.Debug(response)
	if resp != nil {
		if resp.ConversationContext != "" {
			response.ContextID = &resp.ConversationContext
		}

		for _, o := range resp.Outputs {
			// extract content from the first choice since this api version only responded with a single string result
			var content string
			if o.Choices != nil && o.Choices[0].Message.Content != "" {
				content = o.Choices[0].Message.Content
			}

			res := content

			if req.GetScrubPII() {
				scrubbed, err = scrubber.ScrubTexts([]string{content})
				if err != nil {
					err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
					a.logger.Debug(err)
					return &runtimev1pb.ConversationResponse{}, err //nolint:staticcheck
				}

				res = scrubbed[0]
			}

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResult{ //nolint:staticcheck
				Result:     res,
				Parameters: request.Parameters,
			})
		}
	}

	return response, nil
}

func (a *Universal) ConverseAlpha2(ctx context.Context, req *runtimev1pb.ConversationRequestAlpha2) (*runtimev1pb.ConversationResponseAlpha2, error) {
	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	// Log component type for debugging
	if _, isMistral := component.(*mistral.Mistral); isMistral {
		a.logger.Debugf("Detected Mistral component: %s", req.GetName())
	}

	// prepare request
	request := &conversation.Request{}
	var err error
	err = kmeta.DecodeMetadata(req.GetMetadata(), request)
	if err != nil {
		return nil, err
	}

	if request.Message == nil {
		request.Message = &[]llms.MessageContent{}
	}

	if len(req.GetInputs()) == 0 {
		err = messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	var scrubber piiscrubber.Scrubber
	scrubber, err = piiscrubber.NewDefaultScrubber()
	if err != nil {
		err = messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponseAlpha2{}, err
	}

	var llmMessages []*llms.MessageContent
	for _, input := range req.GetInputs() {
		for _, message := range input.GetMessages() {
			var (
				langchainMsg llms.MessageContent
				scrubbed     []string
			)

			if message.GetMessageTypes() == nil {
				err = messages.ErrConversationInvalidParams.WithFormat(req.GetName(), errors.New("message type cannot be nil"))
				a.logger.Debug(err)
				return nil, err
			}

			// Openai allows roles to be passed in; however,
			// we make them implicit in the backend setting this field based on the input msg type using the langchain role types.
			switch msg := message.GetMessageTypes().(type) {
			case *runtimev1pb.ConversationMessage_OfDeveloper:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfDeveloper.GetContent() {
					text := content.GetText()
					if input.GetScrubPii() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextContent{
						Text: text,
					})
				}

				langchainMsg = llms.MessageContent{
					// mapped to human based on these options:
					// https://github.com/tmc/langchaingo/blob/main/llms/chat_messages.go#L18
					Role:  llms.ChatMessageTypeHuman,
					Parts: parts,
				}

			case *runtimev1pb.ConversationMessage_OfSystem:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfSystem.GetContent() {
					text := content.GetText()
					if input.GetScrubPii() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextContent{
						Text: text,
					})
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeSystem,
					Parts: parts,
				}

			case *runtimev1pb.ConversationMessage_OfUser:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfUser.GetContent() {
					text := content.GetText()
					if input.GetScrubPii() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextContent{
						Text: text,
					})
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeHuman,
					Parts: parts,
				}

			case *runtimev1pb.ConversationMessage_OfAssistant:
				var parts []llms.ContentPart
				for _, content := range msg.OfAssistant.GetContent() {
					text := content.GetText()

					// scrub inputs
					if input.GetScrubPii() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextContent{
						Text: text,
					})
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeAI,
					Parts: parts,
				}

				for _, tool := range msg.OfAssistant.GetToolCalls() {
					if tool.ToolTypes == nil {
						err = messages.ErrConversationInvalidParams.WithFormat(req.GetName(), errors.New("tool types cannot be nil"))
						a.logger.Debug(err)
						return nil, err
					}
					toolCall := llms.ToolCall{
						ID:   tool.GetId(),
						Type: string(llms.ChatMessageTypeFunction),
						FunctionCall: &llms.FunctionCall{
							Name:      tool.GetFunction().GetName(),
							Arguments: tool.GetFunction().GetArguments(),
						},
					}

					// handle mistral edge case on handling tool call message
					// where it expects a text message instead of a tool call message
					if _, ok := component.(*mistral.Mistral); ok {
						langchainMsg.Parts = append(langchainMsg.Parts, mistral.CreateToolCallPart(&toolCall))
					} else {
						langchainMsg.Parts = append(langchainMsg.Parts, toolCall)
					}
				}

			case *runtimev1pb.ConversationMessage_OfTool:
				toolID := ""
				if msg.OfTool.ToolId != nil {
					toolID = msg.OfTool.GetToolId()
				}

				var parts []llms.ContentPart
				for _, content := range msg.OfTool.GetContent() {
					text := content.GetText()

					// scrub inputs
					if input.GetScrubPii() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						text = scrubbed[0]
					}

					toolCallResponse := llms.ToolCallResponse{
						ToolCallID: toolID,
						Content:    text,
						Name:       msg.OfTool.GetName(),
					}

					parts = append(parts, toolCallResponse)
				}

				// handle mistral edge case on handling tool call response message
				// where it expects a text message instead of a tool call response message
				if _, ok := component.(*mistral.Mistral); ok {
					langchainMsg = mistral.CreateToolResponseMessage(parts...)
				} else {
					langchainMsg = llms.MessageContent{
						Role:  llms.ChatMessageTypeTool,
						Parts: parts,
					}
				}

			default:
				err = messages.ErrConversationInvalidParams.WithFormat(req.GetName())
				a.logger.Debug(err)
				return nil, err
			}
			llmMessages = append(llmMessages, &langchainMsg)
		}
	}

	if len(llmMessages) > 0 {
		requestMessages := make([]llms.MessageContent, len(llmMessages))
		for i, msg := range llmMessages {
			requestMessages[i] = *msg
		}
		request.Message = &requestMessages
	}

	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextId()
	request.Temperature = req.GetTemperature()
	toolChoice := req.GetToolChoice()
	tools := req.GetTools()

	// set default tool choice to auto if not specified and tools are available
	if toolChoice == "" && len(tools) > 0 {
		toolChoice = "auto"
	}

	// validate tool choice
	switch toolChoice {
	case "auto", "none", "":
	case "required":
		if len(tools) == 0 {
			err = messages.ErrConversationInvalidParams.WithFormat(req.GetName(), "tool choice must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used")
			a.logger.Debug(err)
			return nil, err
		}
	default:
		// user chose a specific tool name that we must validate.
		// for now, tool name passed in must match casing/syntax specified.
		if tools != nil {
			toolNameFound := false
			for _, tool := range tools {
				switch t := tool.GetToolTypes().(type) {
				case *runtimev1pb.ConversationTools_Function:
					if toolChoice == t.Function.GetName() {
						toolNameFound = true
						break
					}
				}
			}
			if !toolNameFound {
				err = messages.ErrConversationInvalidParams.WithFormat(req.GetName(), "tool choice selected was not found. Must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used")
				a.logger.Debug(err)
				return nil, err
			}
		}
	}

	if toolChoice != "" {
		request.ToolChoice = &toolChoice
	}

	if tools := req.GetTools(); tools != nil {
		availableTools := make([]llms.Tool, 0, len(tools))
		for _, tool := range tools {
			switch t := tool.GetToolTypes().(type) {
			case *runtimev1pb.ConversationTools_Function:
				langchainTool := llms.Tool{
					Type: "function",
					Function: &llms.FunctionDefinition{
						Name:        t.Function.GetName(),
						Description: t.Function.GetDescription(),
						Parameters:  t.Function.GetParameters().AsMap(),
					},
				}
				availableTools = append(availableTools, langchainTool)
			}
		}
		request.Tools = &availableTools
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.Response](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	// transform v2 proto -> v1 component request
	resp, err := policyRunner(func(ctx context.Context) (*conversation.Response, error) {
		rResp, rErr := component.Converse(ctx, request)
		return rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)
	diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), err == nil, elapsed)

	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponseAlpha2{}, err
	}

	// handle response
	response := &runtimev1pb.ConversationResponseAlpha2{}
	a.logger.Debug(response)
	if resp != nil {
		if resp.ConversationContext != "" {
			response.ContextId = &resp.ConversationContext
		}

		for _, o := range resp.Outputs {
			var resultingChoices []*runtimev1pb.ConversationResultChoices

			for _, choice := range o.Choices {
				resultMessage := &runtimev1pb.ConversationResultMessage{}

				// handle text result
				if choice.Message.Content != "" {
					content := choice.Message.Content
					if req.GetScrubPii() {
						var scrubbed []string
						scrubbed, err = scrubber.ScrubTexts([]string{content})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseAlpha2{}, err
						}
						content = scrubbed[0]
					}
					resultMessage.Content = content
				}

				// handle tool call results for user to then execute locally
				if choice.Message.ToolCallRequest != nil {
					for _, toolCall := range *choice.Message.ToolCallRequest {
						resultingToolCall := &runtimev1pb.ConversationToolCalls{
							ToolTypes: &runtimev1pb.ConversationToolCalls_Function{
								Function: &runtimev1pb.ConversationToolCallsOfFunction{
									Name:      toolCall.FunctionCall.Name,
									Arguments: toolCall.FunctionCall.Arguments,
								},
							},
						}
						if toolCall.ID != "" {
							resultingToolCall.Id = &toolCall.ID
						}
						resultMessage.ToolCalls = append(resultMessage.ToolCalls, resultingToolCall)
					}
				}

				resultingChoice := &runtimev1pb.ConversationResultChoices{
					FinishReason: choice.FinishReason,
					Index:        choice.Index,
					Message:      resultMessage,
				}

				resultingChoices = append(resultingChoices, resultingChoice)
			}

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResultAlpha2{
				Choices: resultingChoices,
			})
		}
	}

	return response, nil
}
