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
	"errors"
	"fmt"
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"
	"github.com/tmc/langchaingo/llms"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/conversation/mistral"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	kmeta "github.com/dapr/kit/metadata"
)

func (a *Universal) ConverseAlpha1(ctx context.Context, req *runtimev1pb.ConversationRequest) (*runtimev1pb.ConversationResponse, error) {
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

	scrubber, err := piiscrubber.NewDefaultScrubber()
	if err != nil {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponse{}, err
	}

	for _, i := range req.GetInputs() {
		msg := i.GetContent()

		if i.GetScrubPII() {
			scrubbed, sErr := scrubber.ScrubTexts([]string{i.GetContent()})
			if sErr != nil {
				sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
				a.logger.Debug(sErr)
				return &runtimev1pb.ConversationResponse{}, sErr
			}

			msg = scrubbed[0]
		}

		c := llms.MessageContent{
			Role: llms.ChatMessageType(i.GetRole()),
			Parts: []llms.ContentPart{
				llms.TextContent{
					Text: msg,
				},
			},
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
			// extract content from the first choice since this api version only responded with a single string result
			var content string
			if o.Choices != nil {
				content = o.Choices[0].Message.Content
			}

			res := content

			if req.GetScrubPII() {
				scrubbed, sErr := scrubber.ScrubTexts([]string{content})
				if sErr != nil {
					sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
					a.logger.Debug(sErr)
					return &runtimev1pb.ConversationResponse{}, sErr
				}

				res = scrubbed[0]
			}

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResult{
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
	err := kmeta.DecodeMetadata(req.GetMetadata(), request)
	if err != nil {
		return nil, err
	}

	if len(req.GetInputs()) == 0 {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	scrubber, err := piiscrubber.NewDefaultScrubber()
	if err != nil {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return &runtimev1pb.ConversationResponseAlpha2{}, err
	}

	var llmMessages []*llms.MessageContent
	for _, input := range req.GetInputs() {
		for _, message := range input.GetMessages() {
			var (
				langchainMsg llms.MessageContent
			)

			// Openai allows roles to be passed in; however,
			// we make them implicit in the backend setting this field based on the input msg type using the langchain role types.
			switch msg := message.GetMessageTypes().(type) {

			case *runtimev1pb.ConversationMessage_OfDeveloper:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfDeveloper.GetContent() {
					text := content.GetText()
					if input.GetScrubPii() {
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
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

			case *runtimev1pb.ConversationMessage_OfSystem:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfSystem.GetContent() {
					text := content.GetText()
					if input.GetScrubPii() {
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
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
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
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
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextPart(text))
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeAI,
					Parts: parts,
				}

				for _, tool := range msg.OfAssistant.GetToolCalls() {
					// TODO: scrub anything?

					role := llms.ChatMessageTypeTool
					tcfaBytes, err := json.Marshal(tool.GetFunction().GetArguments())
					if err != nil {
						a.logger.Debug(err)
						return &runtimev1pb.ConversationResponseAlpha2{}, err
					}

					toolCall := &llms.ToolCall{
						ID:   tool.GetId(),
						Type: string(role), // double check if this is right or should be just "function"
						FunctionCall: &llms.FunctionCall{
							Name:      tool.GetFunction().GetName(),
							Arguments: string(tcfaBytes),
						},
					}

					// handle mistral edge case on handling tool call message
					// where it expects a text message instead of a tool call message
					if _, ok := component.(*mistral.Mistral); ok {
						langchainMsg.Parts = append(parts, mistral.CreateToolCallPart(toolCall))
					} else {
						langchainMsg.Parts = append(langchainMsg.Parts, toolCall)
					}
				}

			case *runtimev1pb.ConversationMessage_OfTool:
				// TODO: scrub anything?

				toolID := ""
				if msg.OfTool.ToolId != nil {
					toolID = msg.OfTool.GetToolId()
				}

				var parts []llms.ContentPart
				for _, content := range msg.OfTool.GetContent() {
					text := content.GetText()

					// scrub inputs
					if input.GetScrubPii() {
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
						}
						text = scrubbed[0]
					}

					toolCallResponse := llms.ToolCallResponse{
						ToolCallID: toolID,
						Content:    text,
						Name:       msg.OfTool.GetName(),
					}

					// handle mistral edge case on handling tool call response message
					// where it expects a text message instead of a tool call response message
					if _, ok := component.(*mistral.Mistral); ok {
						langchainMsg = mistral.CreateToolResponseMessage(toolCallResponse)
					} else {
						langchainMsg = llms.MessageContent{
							Role:  llms.ChatMessageTypeTool,
							Parts: parts,
						}
					}
				}

			default:
				// TODO(@Sicoyle): should I create custom conversation err types?
				return &runtimev1pb.ConversationResponseAlpha2{}, errors.New("message type is invalid")
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

	// set default tool choice
	if toolChoice == "" {
		if len(tools) > 0 {
			toolChoice = "auto"
		} else {
			toolChoice = "none"
		}
	}

	// validate tool choice
	switch toolChoice {
	case "auto", "none":
	case "required":
		if len(tools) <= 0 {
			return &runtimev1pb.ConversationResponseAlpha2{}, errors.New("tool choice must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used")
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
				return &runtimev1pb.ConversationResponseAlpha2{}, errors.New("tool choice must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used")
			}
		}
	}

	request.ToolChoice = &toolChoice

	if tools := req.GetTools(); tools != nil {
		availableTools := []llms.Tool{}
		for _, tool := range tools {
			switch t := tool.GetToolTypes().(type) {
			case *runtimev1pb.ConversationTools_Function:
				langchainTool := llms.Tool{
					Type: "function",
					Function: &llms.FunctionDefinition{
						Name:        t.Function.GetName(),
						Description: t.Function.GetDescription(),
						Parameters:  t.Function.GetParameters(),
					},
				}
				availableTools = append(availableTools, langchainTool)
			}
		}
		request.Tools = &availableTools
	}

	// do call
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
						scrubbed, sErr := scrubber.ScrubTexts([]string{content})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseAlpha2{}, sErr
						}
						content = scrubbed[0]
					}
					resultMessage.Content = content
				}

				// handle tool call results for user to then execute locally
				if choice.Message.ToolCallRequest != nil {
					for _, toolCall := range *choice.Message.ToolCallRequest {
						var argsMap map[string]any
						arguments := make(map[string]*anypb.Any)

						// some tools may or may not have arguments
						if toolCall.FunctionCall.Arguments != "" {
							err := json.Unmarshal([]byte(toolCall.FunctionCall.Arguments), &argsMap)
							if err != nil {
								return &runtimev1pb.ConversationResponseAlpha2{}, fmt.Errorf("failed to unmarshal tool call arguments: %w", err)
							}
							// convert parsed arguments to output format
							for k, v := range argsMap {
								if vBytes, err := json.Marshal(v); err == nil {
									arguments[k] = &anypb.Any{
										Value: vBytes,
									}
								}
							}
						}

						resultingToolCall := &runtimev1pb.ConversationToolCalls{
							ToolTypes: &runtimev1pb.ConversationToolCalls_Function{
								Function: &runtimev1pb.ConversationToolCallsOfFunction{
									Name:      toolCall.FunctionCall.Name,
									Arguments: arguments,
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
