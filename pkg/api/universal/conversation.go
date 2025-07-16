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

	for _, input := range req.GetInputs() {
		for messageIndex, message := range input.GetMessages() {
			var (
				content string
				role    string

				// optional based on msg type below
				refusal   *string
				toolCalls []*conversation.ConversationInputToolCalls
				toolId    *string
			)

			// Openai allows roles to be passed in; however,
			// we make them implicit in the backend setting this field based on the input msg type.
			// TODO(@Sicoyle): evaluate where i should put consts like this.
			switch msg := message.GetMessageTypes().(type) {
			case *runtimev1pb.ConversationMessage_OfUser:
				content = msg.OfUser.GetContent()[messageIndex].GetText().GetValue()
				role = "user"
			case *runtimev1pb.ConversationMessage_OfSystem:
				content = msg.OfSystem.GetContent()[messageIndex].GetText().GetValue()
				role = "system"
			case *runtimev1pb.ConversationMessage_OfAssistant:
				for _, tool := range msg.OfAssistant.GetToolCalls() {
					content = msg.OfAssistant.GetContent()[messageIndex].GetText().GetValue()
					role = "assistant"
					refusal = &msg.OfAssistant.Refusal.Value
					tcf := tool.GetFunction()
					tcfaBytes, err := json.Marshal(tcf.GetArguments())
					if err != nil {
						a.logger.Debug(err)
						return &runtimev1pb.ConversationResponseV1Alpha2{}, err
					}
					tcfaBytesString := string(tcfaBytes)

					toolCalls = append(toolCalls, &conversation.ConversationInputToolCalls{
						Id: tool.GetId().String(),
						ToolCallFunction: conversation.ToolCallFunction{
							Name:      tcf.GetName().String(),
							Arguments: &tcfaBytesString,
						},
					})
				}

			case *runtimev1pb.ConversationMessage_OfTool:
				content = msg.OfTool.GetContent()[messageIndex].GetText().GetValue()
				role = "tool"
				toolId = &msg.OfTool.GetToolId().Value
			default:
				// TODO(@Sicoyle): should I create custom conversation err types?
				return &runtimev1pb.ConversationResponseV1Alpha2{}, errors.New("message type is invalid")
			}

			if input.GetScrubPII() {
				scrubbed, sErr := scrubber.ScrubTexts([]string{content})
				if sErr != nil {
					sErr = messages.ErrConversationInvoke.WithFormat(req.GetName(), sErr.Error())
					a.logger.Debug(sErr)
					return &runtimev1pb.ConversationResponseV1Alpha2{}, sErr
				}
				content = scrubbed[messageIndex]
			}

			c := conversation.ConversationInputV1Alpha2{
				Message: content,
				Role:    conversation.Role(role),
			}

			// TODO(@Sicoyle): wrap this to also only do this if it's message of an assistant type
			// and move c creation into diff types instead of this setup.
			if refusal != nil {
				c.Refusal = refusal
			}

			if len(toolCalls) > 0 {
				c.ToolCalls = toolCalls
			}

			if toolId != nil {
				c.ToolId = toolId
			}

			request.Inputs = append(request.Inputs, c)
		}
	}

	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextId()
	request.Temperature = req.GetTemperature()

	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*conversation.ConversationResponseV1Alpha2](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*conversation.ConversationResponseV1Alpha2, error) {
		// HERE SAM fixing in Converse()!
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

		for _, o := range resp.Outputs {
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
					FinishReason: &wrapperspb.StringValue{Value: "TODO"},
					Index:        0, // TODO!!
				},
				Parameters: o.Parameters,
			})
		}
	}

	return response, nil
}
