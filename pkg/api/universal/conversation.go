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
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/conversation/mistral"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	kmeta "github.com/dapr/kit/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"
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
			res := o.Result

			if req.GetScrubPII() {
				scrubbed, sErr := scrubber.ScrubTexts([]string{o.Result})
				if sErr != nil {
					sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
					a.logger.Debug(sErr)
					return &runtimev1pb.ConversationResponse{}, sErr
				}

				res = scrubbed[0]
			}

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResult{
				Result:     res,
				Parameters: o.Parameters,
			})
		}
	}

	return response, nil
}

func (a *Universal) ConverseV1Alpha2(ctx context.Context, req *runtimev1pb.ConversationRequestV1Alpha2) (*runtimev1pb.ConversationResponseV1Alpha2, error) {
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
		return &runtimev1pb.ConversationResponseV1Alpha2{}, err
	}
	// TODO: double check that in case of input of tool call response that i properly translate to llms.ToolCallResponse msg type
	// TODO: sam to double check on nil ptr checks

	var llmMessages []*llms.MessageContent
	for _, input := range req.GetInputs() {
		for _, message := range input.GetMessages() {
			var (
				langchainMsg llms.MessageContent
				scrubbed     []string
			)

			if message.GetMessageTypes() == nil {
				err = messages.ErrConversationInvalidParams.WithFormat(req.GetName())
				a.logger.Debug(err)
				return nil, err
			}

			// Openai allows roles to be passed in; however,
			// we make them implicit in the backend setting this field based on the input msg type using the langchain role types.
			switch msg := message.GetMessageTypes().(type) {

			case *runtimev1pb.ConversationMessage_OfUser:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfUser.GetContent() {
					text := content.GetText().GetValue()
					if input.GetScrubPII() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, err
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
					text := content.GetText().GetValue()
					if input.GetScrubPII() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, err
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

			case *runtimev1pb.ConversationMessage_OfDeveloper:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfDeveloper.GetContent() {
					text := content.GetText().GetValue()
					if input.GetScrubPII() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, err
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

			case *runtimev1pb.ConversationMessage_OfAssistant:
				var parts []llms.ContentPart
				for _, content := range msg.OfAssistant.GetContent() {
					text := content.GetText().GetValue()

					// scrub inputs
					if input.GetScrubPII() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, err
						}
						text = scrubbed[0]
					}
					parts = append(parts, llms.TextPart(text))
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeAI, // TODO: double check this role
					Parts: parts,
				}

				if msg.OfAssistant.Refusal != nil {
					langchainMsg.Parts = append(langchainMsg.Parts, llms.TextPart(msg.OfAssistant.Refusal.Value))
				}

				for _, tool := range msg.OfAssistant.GetToolCalls() {
					// TODO: scrub anything?

					role := llms.ChatMessageTypeTool
					var tcfaBytes []byte
					tcfaBytes, err = json.Marshal(tool.GetFunction().GetArguments())
					if err != nil {
						a.logger.Debug(err)
						return &runtimev1pb.ConversationResponseV1Alpha2{}, err
					}

					// handle mistral edge case on handling tool call message
					// where it expects a text message instead of a tool call message
					// if _, ok := component.(*mistral.Mistral); ok {
					// 	a.logger.Debugf("Processing Mistral tool call: %s(%s)",
					// 		tool.GetFunction().GetName().String(),
					// 		string(tcfaBytes))

					// 	toolCall := &llms.ToolCall{
					// 		ID:   tool.GetId().String(),
					// 		Type: "function",
					// 		FunctionCall: &llms.FunctionCall{
					// 			Name:      tool.GetFunction().GetName().String(),
					// 			Arguments: string(tcfaBytes),
					// 		},
					// 	}

					// 	langchainMsg.Parts = append(langchainMsg.Parts, mistral.CreateMistralCompatibleToolCall(toolCall)...)
					// } else {
					// For other providers, use standard tool call
					toolCall := &llms.ToolCall{
						ID:   tool.GetId().String(),
						Type: string(role), // double check if this is right or should be just "function"
						FunctionCall: &llms.FunctionCall{
							Name:      tool.GetFunction().GetName().String(),
							Arguments: string(tcfaBytes),
						},
					}
					langchainMsg.Parts = append(langchainMsg.Parts, toolCall)
					// }
				}

			case *runtimev1pb.ConversationMessage_OfTool:
				// TODO: scrub anything?

				toolID := ""
				if msg.OfTool.ToolId != nil {
					toolID = msg.OfTool.ToolId.GetValue()
				}

				toolName := ""
				if msg.OfTool.Name != nil {
					toolName = msg.OfTool.Name.GetValue()
				}

				var parts []llms.ContentPart
				for _, content := range msg.OfTool.GetContent() {
					text := content.GetText().GetValue()

					// scrub inputs
					if input.GetScrubPII() {
						scrubbed, err = scrubber.ScrubTexts([]string{text})
						if err != nil {
							err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
							a.logger.Debug(err)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, err
						}
						text = scrubbed[0]
					}

					// wip but pushing for cassie
					// handle mistral edge case on handling tool call response message
					// where it expects a text message instead of a tool call response message
					// if _, ok := component.(*mistral.Mistral); ok {
					// 	a.logger.Debugf("Processing Mistral tool response: ID=%s, Name=%s, Content=%s",
					// 		toolID, toolName, text)

					// 	toolResponse := llms.ToolCallResponse{
					// 		ToolCallID: toolID,
					// 		Content:    text,
					// 		Name:       toolName,
					// 	}

					// 	parts = append(parts, mistral.CreateMistralCompatibleToolResponse(toolResponse)...)
					// } else {
					parts = append(parts, llms.ToolCallResponse{
						ToolCallID: toolID,
						Content:    text,
						Name:       toolName,
					})
					// }
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeTool,
					Parts: parts,
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

	if tools := req.GetTools(); tools != nil {
		availableTools := []llms.Tool{}
		for _, tool := range tools {
			switch t := tool.GetToolTypes().(type) {
			case *runtimev1pb.ConversationTools_Function:
				langchainTool := llms.Tool{
					Type: "function",
					Function: &llms.FunctionDefinition{
						Name:        t.Function.GetName().GetValue(),
						Description: t.Function.GetDescription().GetValue(),
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
		return &runtimev1pb.ConversationResponseV1Alpha2{}, err
	}

	// handle response
	response := &runtimev1pb.ConversationResponseV1Alpha2{}
	a.logger.Debug(response)
	if resp != nil {
		if resp.ConversationContext != "" {
			response.ContextId = &resp.ConversationContext
		}

		for i, o := range resp.Outputs {
			res := o.Result

			if req.GetScrubPii() {
				scrubbed, sErr := scrubber.ScrubTexts([]string{o.Result})
				if sErr != nil {
					sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
					a.logger.Debug(sErr)
					return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
				}

				res = scrubbed[0]
			}

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResultV1Alpha2{
				Choices: &runtimev1pb.ConversationResultChoices{
					Message:      &wrapperspb.StringValue{Value: res},
					FinishReason: &wrapperspb.StringValue{Value: o.StopReason},
					Index:        int64(i),
				},
				Parameters: o.Parameters,
			})
		}
	}

	return response, nil
}
