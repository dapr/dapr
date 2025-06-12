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

	"github.com/dapr/components-contrib/conversation"
)

// Example implementation showing how a conversation component could support streaming
// This would be implemented in components-contrib, not in Dapr runtime

/*
Example: OpenAI component with streaming support

type OpenAIWithStreaming struct {
	*openai.OpenAI  // Embed existing OpenAI component
	llm llms.Model  // LangChain Go model
}

// ConverseStream implements the StreamingCapable interface
func (o *OpenAIWithStreaming) ConverseStream(ctx context.Context, r *conversation.ConversationRequest, streamFunc func(ctx context.Context, chunk []byte) error) (*conversation.ConversationResponse, error) {
	// Convert inputs to LangChain Go messages (same as existing Converse method)
	messages := make([]llms.MessageContent, 0, len(r.Inputs))
	for _, input := range r.Inputs {
		role := conversation.ConvertLangchainRole(input.Role)
		messages = append(messages, llms.MessageContent{
			Role: role,
			Parts: []llms.ContentPart{
				llms.TextPart(input.Message),
			},
		})
	}

	// Prepare options
	opts := []llms.CallOption{}
	if r.Temperature > 0 {
		opts = append(opts, conversation.LangchainTemperature(r.Temperature))
	}

	// Add streaming function - THIS IS THE KEY DIFFERENCE
	opts = append(opts, llms.WithStreamingFunc(streamFunc))

	// Call LangChain Go with streaming
	resp, err := o.llm.GenerateContent(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}

	// Convert response back to Dapr format
	outputs := make([]conversation.ConversationResult, 0, len(resp.Choices))
	for i := range resp.Choices {
		outputs = append(outputs, conversation.ConversationResult{
			Result:     resp.Choices[i].Content,
			Parameters: r.Parameters,
		})
	}

	return &conversation.ConversationResponse{
		Outputs: outputs,
		ConversationContext: r.ConversationContext,
	}, nil
}

Usage in components-contrib:
1. Add ConverseStream method to existing components
2. Use llms.WithStreamingFunc in the call to llm.GenerateContent
3. The streamFunc parameter will be called by LangChain Go for each chunk
4. Dapr runtime will automatically detect and use streaming if available

This approach:
- ✅ Requires minimal changes to existing components
- ✅ Leverages LangChain Go's native streaming support
- ✅ Maintains backward compatibility (components without ConverseStream work normally)
- ✅ Provides real streaming without buffering complete responses
- ✅ Integrates seamlessly with Dapr's PII scrubbing pipeline
*/

// StreamingExampleInterface shows what the enhanced interface would look like
type StreamingExampleInterface interface {
	conversation.Conversation // Embed existing interface

	// ConverseStream enables true streaming via LangChain Go's WithStreamingFunc
	ConverseStream(ctx context.Context, req *conversation.ConversationRequest, streamFunc func(ctx context.Context, chunk []byte) error) (*conversation.ConversationResponse, error)
}
