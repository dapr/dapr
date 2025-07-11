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
	"context"
	"errors"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"

	contribConverse "github.com/dapr/components-contrib/conversation"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/logger"
)

// StreamingPipeline handles the streaming conversation pipeline
type StreamingPipeline struct {
	Middleware         []StreamingMiddleware
	logger             logger.Logger
	simulatedChunkSize int
}

// StreamingMiddleware interface for processing streaming chunks with error handling
type StreamingMiddleware interface {
	ProcessChunk(chunk []byte) ([]byte, error)
	Flush() ([]byte, error)
}

// NewStreamingPipelineImpl creates a new streaming pipeline
func NewStreamingPipelineImpl(logger logger.Logger) *StreamingPipeline {
	return &StreamingPipeline{
		Middleware:         make([]StreamingMiddleware, 0),
		logger:             logger,
		simulatedChunkSize: SimulatedStreamingChunkSize,
	}
}

// AddMiddleware adds a Middleware to the pipeline
func (p *StreamingPipeline) AddMiddleware(middleware StreamingMiddleware) {
	if middleware == nil {
		return
	}

	p.Middleware = append(p.Middleware, middleware)
}

// ProcessStream handles the streaming pipeline from LangChain Go to gRPC stream
func (p *StreamingPipeline) ProcessStream(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	component contribConverse.Conversation,
	request *contribConverse.ConversationRequest,
	componentName string,
	scrubber piiscrubber.Scrubber,
) (*runtimev1pb.ConversationUsage, error) {
	// Try to get the underlying LangChain Go model for streaming support
	if streamer, ok := component.(contribConverse.StreamingConversation); ok {
		usage, err := p.processRealStreaming(ctx, stream, streamer, request, componentName, scrubber)
		if err == nil {
			return usage, nil
		}
		if !(errors.Is(err, contribConverse.ErrStreamingNotSupported) || errors.Is(err, contribConverse.ErrToolCallStreamingNotSupported)) {
			return nil, err
		}
	}

	// Fallback: simulate streaming with existing Converse method
	return p.processSimulatedStreaming(ctx, stream, component, request, componentName, scrubber)
}

// processRealStreaming handles true LangChain Go streaming
func (p *StreamingPipeline) processRealStreaming(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	streamer contribConverse.StreamingConversation,
	request *contribConverse.ConversationRequest,
	componentName string,
	scrubber piiscrubber.Scrubber,
) (*runtimev1pb.ConversationUsage, error) {
	var finalResp *contribConverse.ConversationResponse
	var streamErr error

	// Create the streaming function that LangChain Go will call for each chunk
	streamFunc := func(chunkCtx context.Context, chunk []byte) error {
		// Process chunk through Middleware chain
		processedChunk, err := p.processChunkThroughMiddleware(chunk)
		if err != nil {
			p.logger.Errorf("Failed to process streaming chunk: %v", err)
			return err
		}

		// Send processed chunk immediately (if any ready)
		if len(processedChunk) > 0 {
			if err := p.sendChunk(stream, processedChunk); err != nil {
				p.logger.Errorf("Failed to send streaming chunk: %v", err)
				return err
			}
			p.logger.Debugf("Sent streaming chunk: %s", string(processedChunk))
		}

		return nil
	}

	// Call the component's streaming method
	finalResp, streamErr = streamer.ConverseStream(ctx, request, streamFunc)

	p.logger.Debugf("Final response: %v", finalResp)

	// Flush any remaining content in Middleware buffers
	if remaining, err := p.flushMiddleware(); err != nil {
		p.logger.Errorf("Failed to flush middleware: %v", err)
		return nil, err
	} else if len(remaining) > 0 {
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

	if finalResp == nil {
		return nil, ErrEmptyResponse
	}

	// Handle tool calls in the final response
	for _, output := range finalResp.Outputs {
		// Extract tool calls from content parts
		var toolCalls []contribConverse.ToolCall
		if len(output.Parts) > 0 {
			toolCalls = contribConverse.ExtractToolCallsFromParts(output.Parts)
		}

		// Check if this output contains tool calls
		if len(toolCalls) > 0 {
			// Convert component tool calls to proto format
			protoToolCalls := ConvertComponentsContribToolCallsToProto(toolCalls)

			// Send tool calls as a special chunk with finish reason
			finishReason := "tool_calls"
			if err := p.sendToolCallChunk(stream, protoToolCalls, finishReason); err != nil {
				p.logger.Errorf("Failed to send tool call chunk: %v", err)
				return nil, err
			}
		}
	}

	outputs, err := ConvertComponentsContribOutputToProto(finalResp.Outputs, scrubber, componentName)
	if err != nil {
		return nil, err
	}

	// Send completion with context and usage stats
	usage := ConvertUsageToProto(finalResp)

	return usage, p.sendComplete(stream, finalResp.Context, usage, outputs)
}

// processSimulatedStreaming falls back to chunking the complete response
func (p *StreamingPipeline) processSimulatedStreaming(
	ctx context.Context,
	stream runtimev1pb.Dapr_ConverseStreamAlpha1Server,
	component contribConverse.Conversation,
	request *contribConverse.ConversationRequest,
	componentName string,
	scrubber piiscrubber.Scrubber,
) (*runtimev1pb.ConversationUsage, error) {
	// Call the regular Converse method
	resp, err := component.Converse(ctx, request)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, ErrEmptyResponse
	}

	var contextID string

	// Simulate streaming by sending the complete response as chunks
	contextID = resp.Context
	if len(resp.Outputs) > 0 {
		for _, output := range resp.Outputs {
			for _, part := range output.Parts {
				if textPart, ok := part.(contribConverse.TextContentPart); ok {
					content := textPart.Text
					for i := 0; i < len(content); i += p.simulatedChunkSize {
						end := min(i+p.simulatedChunkSize, len(content))
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
	usage := ConvertUsageToProto(resp)
	outputs, err := ConvertComponentsContribOutputToProto(resp.Outputs, scrubber, componentName)
	if err != nil {
		return nil, err
	}
	return usage, p.sendComplete(stream, contextID, usage, outputs)
}

// processChunkThroughMiddleware processes a chunk through all Middleware (eg PII scrubber)
func (p *StreamingPipeline) processChunkThroughMiddleware(chunk []byte) ([]byte, error) {
	processed := chunk
	for _, middleware := range p.Middleware {
		var err error
		processed, err = middleware.ProcessChunk(processed)
		if err != nil {
			p.logger.Errorf("Failed to process chunk through middleware: %v", err)
			return nil, err
		}
	}
	return processed, nil
}

// flushMiddleware flushes all Middleware and returns any remaining content
func (p *StreamingPipeline) flushMiddleware() ([]byte, error) {
	var result []byte
	for _, middleware := range p.Middleware {
		if remaining, err := middleware.Flush(); err != nil {
			p.logger.Errorf("Failed to flush middleware: %v", err)
			return nil, err
		} else if len(remaining) > 0 {
			result = append(result, remaining...)
		}
	}
	return result, nil
}

// sendChunk sends a content chunk via the gRPC stream
func (p *StreamingPipeline) sendChunk(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, content []byte) error {
	chunk := &runtimev1pb.ConversationStreamChunk{}

	// Add content as parts for rich content support
	// TODO: properly handle tool calls in the content parts
	// we are only sending the text content for now
	if len(content) > 0 {
		chunk.Content = []*runtimev1pb.ConversationContent{
			{
				ContentType: &runtimev1pb.ConversationContent_Text{
					Text: &runtimev1pb.ConversationText{
						Text: string(content),
					},
				},
			},
		}
	}

	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Chunk{
			Chunk: chunk,
		},
	})
}

// sendToolCallChunk sends tool calls via the gRPC stream
func (p *StreamingPipeline) sendToolCallChunk(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, toolCalls []*runtimev1pb.ConversationToolCall, finishReason string) error {
	chunk := &runtimev1pb.ConversationStreamChunk{}

	// Add tool calls as parts for rich content support
	parts := make([]*runtimev1pb.ConversationContent, 0, len(toolCalls)) // Pre-allocate with exact capacity
	for _, toolCall := range toolCalls {
		parts = append(parts, &runtimev1pb.ConversationContent{
			ContentType: &runtimev1pb.ConversationContent_ToolCall{
				ToolCall: toolCall,
			},
		})
	}
	chunk.Content = parts

	if finishReason != "" {
		chunk.FinishReason = &finishReason
	}

	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Chunk{
			Chunk: chunk,
		},
	})
}

// sendComplete sends the completion message with usage stats and outputs
// should be the last message sent to the client
func (p *StreamingPipeline) sendComplete(stream runtimev1pb.Dapr_ConverseStreamAlpha1Server, contextID string, usage *runtimev1pb.ConversationUsage, outputs []*runtimev1pb.ConversationResult) error {
	complete := &runtimev1pb.ConversationStreamEOF{
		Usage:   usage,
		Results: outputs,
	}
	if contextID != "" {
		complete.ContextId = &contextID
	}

	return stream.Send(&runtimev1pb.ConversationStreamResponse{
		ResponseType: &runtimev1pb.ConversationStreamResponse_Complete{
			Complete: complete,
		},
	})
}
