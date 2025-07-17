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
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
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

		c := conversation.ConversationInput{
			Message: msg,
			Role:    conversation.Role(i.GetRole()),
		}

		request.Inputs = append(request.Inputs, c)
	}

	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextID()
	request.Temperature = req.GetTemperature()

	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.ConversationResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*conversation.ConversationResponse, error) {
		rResp, rErr := component.Converse(ctx, request)
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

	// prepare request
	request := &conversation.ConversationRequestV1Alpha2{}
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
		return &runtimev1pb.ConversationResponseV1Alpha2{}, err
	}
	// TODO: double check that in case of input of tool call response that i properly translate to llms.ToolCallResponse msg type
	// TODO: sam to double check on nil ptr checks

	for _, input := range req.GetInputs() {
		for _, message := range input.GetMessages() {
			var (
				langchainMsg llms.MessageContent
			)

			// Openai allows roles to be passed in; however,
			// we make them implicit in the backend setting this field based on the input msg type using the langchain role types.
			switch msg := message.GetMessageTypes().(type) {

			case *runtimev1pb.ConversationMessage_OfUser:
				var parts []llms.ContentPart

				// scrub inputs
				for _, content := range msg.OfUser.GetContent() {
					text := content.GetText().GetValue()
					if input.GetScrubPII() {
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
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
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
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

			case *runtimev1pb.ConversationMessage_OfAssistant:
				var parts []llms.ContentPart
				for _, content := range msg.OfAssistant.GetContent() {
					text := content.GetText().GetValue()

					// scrub inputs
					if input.GetScrubPII() {
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
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
					tcfaBytes, err := json.Marshal(tool.GetFunction().GetArguments())
					if err != nil {
						a.logger.Debug(err)
						return &runtimev1pb.ConversationResponseV1Alpha2{}, err
					}

					toolCall := &llms.ToolCall{
						ID:   tool.GetId().String(),
						Type: string(role), // double check this role
						FunctionCall: &llms.FunctionCall{
							Name:      tool.GetFunction().GetName().String(),
							Arguments: string(tcfaBytes),
						},
					}

					langchainMsg = llms.MessageContent{
						Role:  role, // doble check this role and if it should be tool or assistant...
						Parts: append(langchainMsg.Parts, toolCall),
					}
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
						scrubbed, sErr := scrubber.ScrubTexts([]string{text})
						if sErr != nil {
							sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
							a.logger.Debug(sErr)
							return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
						}
						text = scrubbed[0]
					}

					parts = append(parts, llms.ToolCallResponse{
						ToolCallID: toolID,
						Content:    text,
						Name:       toolName,
					})
				}

				langchainMsg = llms.MessageContent{
					Role:  llms.ChatMessageTypeTool,
					Parts: parts,
				}

			default:
				// TODO(@Sicoyle): should I create custom conversation err types?
				return &runtimev1pb.ConversationResponseV1Alpha2{}, errors.New("message type is invalid")
			}
			request.Message = append(request.Message, &langchainMsg)

		}
	}
	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextId()
	request.Temperature = req.GetTemperature()

	if tools := req.GetTools(); tools != nil {
		availableTools := []*llms.Tool{}
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

				availableTools = append(availableTools, &langchainTool)
			}
		}
		request.Tools = availableTools
	}
	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.ConversationResponseV1Alpha2](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*conversation.ConversationResponseV1Alpha2, error) {
		rResp, rErr := component.ConverseV1Alpha2(ctx, request)
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
