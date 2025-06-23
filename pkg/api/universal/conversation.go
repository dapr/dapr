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
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	"github.com/dapr/components-contrib/conversation"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	kmeta "github.com/dapr/kit/metadata"
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

		c := conversation.ConversationInput{
			Message: msg,
			Role:    conversation.Role(i.GetRole()),
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

			response.Outputs = append(response.GetOutputs(), &runtimev1pb.ConversationResult{
				Result:     res,
				Parameters: o.Parameters,
			})
		}
		response.Usage = usage
	}

	return response, nil
}
