/*
Copyright 2024 The Dapr Authors
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
	"fmt"
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	contribConverse "github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/conversation"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

func (a *Universal) ConverseAlpha1(ctx context.Context, req *runtimev1pb.ConversationRequest) (*runtimev1pb.ConversationResponse, error) {
	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return nil, err
	}

	if len(req.GetInputs()) == 0 {
		return nil, messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	var inputScrubber, outputScrubber piiscrubber.Scrubber
	needsInputPIIScrubbing := conversation.NeedsInputScrubber(req)
	needsOutputPIIScrubbing := conversation.NeedsOutputScrubber(req)

	if needsInputPIIScrubbing || needsOutputPIIScrubbing {
		scrubber, err := piiscrubber.NewDefaultScrubber()
		if err != nil {
			return nil, messages.ErrConversationScrubbingFailed.WithFormat(req.GetName(), err.Error())
		}
		if needsInputPIIScrubbing {
			inputScrubber = scrubber
		}
		if needsOutputPIIScrubbing {
			outputScrubber = scrubber
		}
	}

	componentReq, err := conversation.CreateComponentRequest(req, inputScrubber)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	policyRunner := resiliency.NewRunner[*contribConverse.ConversationResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*contribConverse.ConversationResponse, error) {
		rResp, rErr := component.Converse(ctx, componentReq)
		return rResp, rErr
	})

	response := &runtimev1pb.ConversationResponse{}
	if resp != nil {
		// usage
		usage := conversation.ConvertUsageToProto(resp)
		diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), err == nil,
			diag.ElapsedSince(start), diag.NonStreamingConversation, int64(usage.GetPromptTokens()), int64(usage.GetCompletionTokens()))
		response.Usage = usage

		// TODO(@Sicoyle): determine if contextID is optional or required
		if resp.Context != "" {
			response.ContextID = &resp.Context
		}

		response.Results, err = conversation.ConvertComponentsContribOutputToProto(resp.Outputs, outputScrubber, req.GetName())
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
		a.logger.Debug(err)
		return nil, err
	}

	a.logger.Infof("ConversationAlpha1 response: %v", response) // TODO(@Sicoyle): delete this line
	return response, nil
}

// ConverseStreamAlpha1 implements the streaming conversation API
func (a *Universal) ConverseStreamAlpha1(req *runtimev1pb.ConversationRequest, stream runtimev1pb.Dapr_ConverseStreamAlpha1Server) error {
	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return err
	}

	if len(req.GetInputs()) == 0 {
		return messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	// TODO(@Sicoyle): maybe create a diff streaming pkg - tbd maybe conversationstreaming pkg instead tbh
	pipeline := conversation.NewStreamingPipelineImpl(a.logger)

	var inputScrubber, outputScrubber piiscrubber.Scrubber
	needsInputPIIScrubbing := conversation.NeedsInputScrubber(req)
	needsOutputPIIScrubbing := conversation.NeedsOutputScrubber(req)
	if needsInputPIIScrubbing || needsOutputPIIScrubbing {
		scrubber, err := piiscrubber.NewDefaultScrubber()
		if err != nil {
			return messages.ErrConversationScrubbingFailed.WithFormat(req.GetName(), err.Error())
		}
		if needsInputPIIScrubbing {
			inputScrubber = scrubber
		}
		if needsOutputPIIScrubbing {
			outputScrubber = scrubber
			// TODO(@Sicoyle): come back to this and the window size stuff
			scrubberMiddleware, err := conversation.NewStreamingPIIScrubber()
			if err != nil {
				return fmt.Errorf("failed to create streaming PII scrubber middleware: %w", err)
			}

			pipeline.AddMiddleware(scrubberMiddleware)
		}
	}

	componentRequest, err := conversation.CreateComponentRequest(req, inputScrubber)
	if err != nil {
		return err
	}

	return a.processConversationStream(stream, component, componentRequest, req.GetName(), outputScrubber, pipeline)
}

func (a *Universal) processConversationStream(
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	component contribConverse.Conversation,
	componentRequest *contribConverse.ConversationRequest,
	componentName string,
	outputScrubber piiscrubber.Scrubber,
	pipeline *conversation.StreamingPipeline,
) error {
	ctx := stream.Context()
	start := time.Now()

	// TODO(@Sicoyle): mv scrubbers to be fields on pipeline instead
	usage, err := pipeline.ProcessStream(ctx, stream, component, componentRequest, componentName, outputScrubber)

	// TODO(@Sicoyle): mv this to the pipeline instead of here?
	elapsed := diag.ElapsedSince(start)
	var promptTokens, completionTokens int64
	if usage != nil {
		promptTokens = int64(usage.GetPromptTokens())
		completionTokens = int64(usage.GetCompletionTokens())
	}
	diag.DefaultComponentMonitoring.ConversationInvoked(ctx, componentName, err == nil, elapsed, diag.StreamingConversation, promptTokens, completionTokens)

	if err != nil {
		// TODO(@Sicoyle): do I need to add a streaming conversation metric increment here?
		a.logger.Debug(err)
	}
	return err
}
