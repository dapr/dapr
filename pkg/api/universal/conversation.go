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

	if len(req.GetInputs()) == 0 {
		return nil, messages.ErrConversationMissingInputs.WithFormat(req.GetName())
	}

	// prepare inputScrubber in case PII scrubbing is enabled (either at request level or input level)
	var inputScrubber, outputScrubber piiscrubber.Scrubber

	// Check if PII scrubbing is needed for inputs or outputs
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

	// Prepare component conversation componentRequest (similar to non-streaming version)
	componentRequest, err := conversation.GetComponentRequest(req, inputScrubber)
	if err != nil {
		return nil, err
	}

	// do call
	start := time.Now()
	policyRunner := resiliency.NewRunner[*contribConverse.ConversationResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(req.GetName(), resiliency.Conversation),
	)

	resp, err := policyRunner(func(ctx context.Context) (*contribConverse.ConversationResponse, error) {
		// Call component with regular interface
		// Components that support tool calling should handle tools from input.Tools
		rResp, rErr := component.Converse(ctx, componentRequest)
		return rResp, rErr
	})
	elapsed := diag.ElapsedSince(start)
	usage := conversation.ConvertUsageToProto(resp)

	// Safe usage extraction for monitoring
	var promptTokens, completionTokens int64
	if usage != nil {
		promptTokens = int64(usage.GetPromptTokens())         //nolint:gosec // Intentional use of int64 for backward compatibility
		completionTokens = int64(usage.GetCompletionTokens()) //nolint:gosec // Intentional use of int64 for backward compatibility
	}
	diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), err == nil, elapsed, diag.NonStreamingConversation, promptTokens, completionTokens)

	if err != nil {
		err = messages.ErrConversationInvoke.WithFormat(req.GetName(), err.Error())
		a.logger.Debug(err)
		return nil, err
	}

	// handle response
	response := &runtimev1pb.ConversationResponse{}
	a.logger.Debug(response)
	if resp != nil {
		if resp.ConversationContext != "" {
			response.ContextID = &resp.ConversationContext
		}

		response.Outputs, err = conversation.ConvertComponentsContribOutputToProto(resp.Outputs, outputScrubber, req.GetName())
		if err != nil {
			return nil, err
		}
		response.Usage = usage
	}

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
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return err
	}

	// Create streaming pipeline
	pipelineImpl := conversation.NewStreamingPipelineImpl(a.logger)

	// prepare non-streaming scrubber in case PII scrubbing is enabled
	var inputScrubber, outputScrubber piiscrubber.Scrubber

	// Check if PII scrubbing is needed for inputs or outputs
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
			scrubberMiddleware, err := conversation.NewStreamingPIIScrubber()
			if err != nil {
				return fmt.Errorf("failed to create streaming PII scrubber middleware: %w", err)
			}
			pipelineImpl.AddMiddleware(scrubberMiddleware)
		}
	}

	// Prepare component conversation componentRequest (similar to non-streaming version)
	componentRequest, err := conversation.GetComponentRequest(req, inputScrubber)
	if err != nil {
		return err
	}

	// Process streaming request
	ctx := stream.Context()
	start := time.Now()
	var usage *runtimev1pb.ConversationUsage
	var retErr error // used in defer to track error
	defer func() {
		elapsed := diag.ElapsedSince(start)
		var promptTokens, completionTokens int64
		if usage != nil {
			promptTokens = int64(usage.GetPromptTokens())         //nolint:gosec // Intentional use of int64 for backward compatibility
			completionTokens = int64(usage.GetCompletionTokens()) //nolint:gosec // Intentional use of int64 for backward compatibility
		}
		diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), retErr == nil, elapsed, diag.StreamingConversation, promptTokens, completionTokens)
	}()

	// we use non-streaming scrubber on the non-streamingoutput
	// we get usage to use in monitoring
	usage, retErr = pipelineImpl.ProcessStream(ctx, stream, component, componentRequest, req.GetName(), outputScrubber)
	return retErr
}
