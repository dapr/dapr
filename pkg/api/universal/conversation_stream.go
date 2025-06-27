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

package universal

import (
	"context"
	"fmt"
	"time"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	"github.com/dapr/components-contrib/conversation"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/logger"
	kmeta "github.com/dapr/kit/metadata"
)

// defaultPIIScrubberWindowSize is the default window size (bytes) for PII scrubbing
// this allows for a look-ahead window of stream data to be scrubbed
// TODO: make this configurable
const defaultPIIScrubberWindowSize = 50

// ConverseStreamAlpha1 implements the streaming conversation API
func (a *Universal) ConverseStreamAlpha1(req *runtimev1pb.ConversationRequest, stream runtimev1pb.Dapr_ConverseStreamAlpha1Server) (retErr error) {
	// Validate component exists
	if a.compStore.ConversationsLen() == 0 {
		err := messages.ErrConversationNotFound
		a.logger.Debug(err)
		return err
	}

	component, ok := a.compStore.GetConversation(req.GetName())
	if !ok {
		err := messages.ErrConversationNotFound.WithFormat(req.GetName())
		a.logger.Debug(err)
		return err
	}

	// Prepare component conversation request (similar to non-streaming version)
	request := &conversation.ConversationRequest{}
	err := kmeta.DecodeMetadata(req.GetMetadata(), request)
	if err != nil {
		return err
	}

	if len(req.GetInputs()) == 0 {
		err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
		a.logger.Debug(err)
		return err
	}

	// Create streaming pipeline
	pipelineImpl := NewStreamingPipelineImpl(a.logger)

	// prepare scrubber in case PII scrubbing is enabled
	var scrubber piiscrubber.Scrubber

	// Check if PII scrubbing is needed for inputs or outputs
	needsInputPIIScrubbing := needsInputScrubber(req)
	needsOutputPIIScrubbing := needsOutputScrubber(req)

	if needsInputPIIScrubbing || needsOutputPIIScrubbing {
		scrubber, err = piiscrubber.NewDefaultScrubber()
		if err != nil {
			err := messages.ErrConversationMissingInputs.WithFormat(req.GetName())
			a.logger.Debug(err)
			return err
		}

		// Add PII scrubbing middleware for streaming output scrubbing
		if needsOutputPIIScrubbing {
			scrubberMiddleware, scrubberErr := NewStreamingPIIScrubber(defaultPIIScrubberWindowSize)
			if scrubberErr != nil {
				return fmt.Errorf("failed to create streaming PII scrubber middleware: %w", scrubberErr)
			}
			pipelineImpl.AddMiddleware(scrubberMiddleware)
		}
	}

	// get inputs from request and scrub them if PII scrubbing is enabled
	request.Inputs, err = getInputsFromRequest(req, scrubber, a.logger)
	if err != nil {
		return err
	}

	request.Parameters = req.GetParameters()
	request.ConversationContext = req.GetContextID()
	request.Temperature = req.GetTemperature()

	// Process streaming request
	ctx := stream.Context()
	start := time.Now()
	var usage *runtimev1pb.ConversationUsage
	defer func() {
		elapsed := diag.ElapsedSince(start)
		diag.DefaultComponentMonitoring.ConversationInvoked(ctx, req.GetName(), retErr == nil, elapsed, diag.StreamingConversation, int64(usage.GetPromptTokens()), int64(usage.GetCompletionTokens()))
	}()
	usage, retErr = pipelineImpl.ProcessStream(ctx, stream, component, request)
	return retErr
}

// StreamingPipelineImpl handles the streaming conversation pipeline
type StreamingPipelineImpl struct {
	middleware []StreamingMiddleware
	logger     logger.Logger
}

// StreamingMiddleware is a simple interface for processing streaming chunks
type StreamingMiddleware interface {
	ProcessChunk(chunk []byte) []byte
	Flush() []byte
}

// NewStreamingPipelineImpl creates a new streaming pipeline
func NewStreamingPipelineImpl(logger logger.Logger) *StreamingPipelineImpl {
	return &StreamingPipelineImpl{
		middleware: make([]StreamingMiddleware, 0),
		logger:     logger,
	}
}

// AddMiddleware adds a middleware to the pipeline
func (p *StreamingPipelineImpl) AddMiddleware(middleware StreamingMiddleware) {
	if middleware == nil {
		return
	}

	p.middleware = append(p.middleware, middleware)
}

// ProcessStream handles the streaming pipeline from LangChain Go to gRPC stream
func (p *StreamingPipelineImpl) ProcessStream(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	component conversation.Conversation,
	request *conversation.ConversationRequest,
) (*runtimev1pb.ConversationUsage, error) {
	// Try to get the underlying LangChain Go model for streaming support
	if streamer, ok := component.(conversation.StreamingConversation); ok {
		return p.processRealStreaming(ctx, stream, streamer, request)
	}

	// Fallback: simulate streaming with existing Converse method
	return p.processSimulatedStreaming(ctx, stream, component, request)
}

// processRealStreaming handles true LangChain Go streaming
func (p *StreamingPipelineImpl) processRealStreaming(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	streamer conversation.StreamingConversation,
	request *conversation.ConversationRequest,
) (*runtimev1pb.ConversationUsage, error) {
	var finalResp *conversation.ConversationResponse
	var streamErr error

	// Create the streaming function that LangChain Go will call for each chunk
	streamFunc := func(chunkCtx context.Context, chunk []byte) error {
		// Process chunk through middleware chain
		processedChunk := p.processChunkThroughMiddleware(chunk)

		// Send processed chunk immediately (if any ready)
		if len(processedChunk) > 0 {
			if err := p.sendChunk(stream, processedChunk); err != nil {
				p.logger.Errorf("Failed to send streaming chunk: %v", err)
				return err
			}
		}

		return nil
	}

	// Call the component's streaming method
	finalResp, streamErr = streamer.ConverseStream(ctx, request, streamFunc)

	// Flush any remaining content in middleware buffers
	if remaining := p.flushMiddleware(); len(remaining) > 0 {
		if sendErr := p.sendChunk(stream, remaining); sendErr != nil {
			p.logger.Errorf("Failed to send final streaming chunk: %v", sendErr)
			// Return the component error if it exists, otherwise the send error
			if streamErr != nil {
				return nil, streamErr
			}
			return nil, sendErr
		}
	}

	if streamErr != nil {
		return nil, streamErr
	}

	// NEW: Handle tool calls in the final response
	if finalResp != nil && len(finalResp.Outputs) > 0 {
		for _, output := range finalResp.Outputs {
			// Check if this output contains tool calls
			if len(output.ToolCalls) > 0 {
				// Convert component tool calls to proto format
				protoToolCalls := convertComponentsContribToolCallsToProto(output.ToolCalls)

				// Send tool calls as a special chunk with finish reason
				if err := p.sendToolCallChunk(stream, protoToolCalls, output.FinishReason); err != nil {
					p.logger.Errorf("Failed to send tool call chunk: %v", err)
					return nil, err
				}
			}
		}
	}

	// Send completion with context and usage stats
	contextID := ""
	var usage *runtimev1pb.ConversationUsage
	if finalResp != nil {
		contextID = finalResp.ConversationContext
		usage = convertUsageToProto(finalResp)
	}
	return usage, p.sendComplete(stream, contextID, usage)
}

// processSimulatedStreaming falls back to chunking the complete response
func (p *StreamingPipelineImpl) processSimulatedStreaming(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	component conversation.Conversation,
	request *conversation.ConversationRequest,
) (*runtimev1pb.ConversationUsage, error) {
	// Call the regular Converse method
	resp, err := component.Converse(ctx, request)
	if err != nil {
		return nil, err
	}

	var contextID string

	// Simulate streaming by sending the complete response as chunks
	if resp != nil {
		contextID = resp.ConversationContext
		if len(resp.Outputs) > 0 {
			for _, output := range resp.Outputs {
				// NEW: Check if this output contains tool calls (prioritize tool calls over content)
				if len(output.ToolCalls) > 0 {
					// Convert component tool calls to proto format
					protoToolCalls := convertComponentsContribToolCallsToProto(output.ToolCalls)

					// Send tool calls as a special chunk with finish reason
					if err := p.sendToolCallChunk(stream, protoToolCalls, output.FinishReason); err != nil {
						return nil, err
					}
				} else if output.Result != "" {
					// Break the result into chunks to simulate streaming (only if no tool calls)
					content := output.Result
					chunkSize := 50 // Send in 50-character chunks
					for i := 0; i < len(content); i += chunkSize {
						end := min(i+chunkSize, len(content))
						chunk := content[i:end]
						if err := p.sendChunk(stream, []byte(chunk)); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}

	// Convert usage information from response
	usage := convertUsageToProto(resp)
	return usage, p.sendComplete(stream, contextID, usage)
}

// processChunkThroughMiddleware processes a chunk through all middleware
func (p *StreamingPipelineImpl) processChunkThroughMiddleware(chunk []byte) []byte {
	processed := chunk
	for _, middleware := range p.middleware {
		processed = middleware.ProcessChunk(processed)
	}
	return processed
}

// flushMiddleware flushes all middleware and returns any remaining content
func (p *StreamingPipelineImpl) flushMiddleware() []byte {
	var result []byte
	for _, middleware := range p.middleware {
		if remaining := middleware.Flush(); len(remaining) > 0 {
			result = append(result, remaining...)
		}
	}
	return result
}

// sendChunk sends a content chunk via the gRPC stream
func (p *StreamingPipelineImpl) sendChunk(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, content []byte) error {
	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Chunk{
			Chunk: &runtimev1pb.ConversationStreamChunk{
				Content: string(content),
			},
		},
	})
}

// NEW: sendToolCallChunk sends tool calls via the gRPC stream
func (p *StreamingPipelineImpl) sendToolCallChunk(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, toolCalls []*runtimev1pb.ToolCall, finishReason string) error {
	chunk := &runtimev1pb.ConversationStreamChunk{
		Content:   "", // No text content when sending tool calls
		ToolCalls: toolCalls,
	}

	if finishReason != "" {
		chunk.FinishReason = &finishReason
	}

	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Chunk{
			Chunk: chunk,
		},
	})
}

// sendComplete sends the completion message
func (p *StreamingPipelineImpl) sendComplete(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, contextID string, usage *runtimev1pb.ConversationUsage) error {
	complete := &runtimev1pb.ConversationStreamComplete{
		Usage: usage,
	}
	if contextID != "" {
		complete.ContextID = &contextID
	}

	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Complete{
			Complete: complete,
		},
	})
}

// convertUsageToProto converts components-contrib UsageInfo to Dapr protobuf ConversationUsage
func convertUsageToProto(resp *conversation.ConversationResponse) *runtimev1pb.ConversationUsage {
	if resp == nil || resp.Usage == nil {
		return nil
	}

	return &runtimev1pb.ConversationUsage{
		PromptTokens:     &resp.Usage.PromptTokens,
		CompletionTokens: &resp.Usage.CompletionTokens,
		TotalTokens:      &resp.Usage.TotalTokens,
	}
}
