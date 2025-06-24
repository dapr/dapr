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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

const (
	fakeConversationComponentName = "fakeConversationComponent"
	fakeStreamingComponentName    = "fakeStreamingComponent"
	fakeToolCallingComponentName  = "fakeToolCallingComponent"
)

// Simple mock middleware for testing
type simpleMockMiddleware struct {
	processedContent string
}

func (m *simpleMockMiddleware) ProcessChunk(chunk []byte) []byte {
	return []byte(m.processedContent)
}

func (m *simpleMockMiddleware) Flush() []byte {
	return []byte(m.processedContent)
}

// Mock conversation component for testing
type mockConversationComponent struct {
	shouldError bool
	response    *conversation.ConversationResponse
}

func (m *mockConversationComponent) Init(ctx context.Context, meta conversation.Metadata) error {
	return nil
}

func (m *mockConversationComponent) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func (m *mockConversationComponent) Converse(ctx context.Context, req *conversation.ConversationRequest) (*conversation.ConversationResponse, error) {
	if m.shouldError {
		return nil, errors.New("mock conversation error")
	}
	return m.response, nil
}

func (m *mockConversationComponent) Close() error {
	return nil
}

// Mock tool calling conversation component that simulates OpenAI-style tool calling

// Mock streaming-capable conversation component
type mockStreamingConversationComponent struct {
	*mockConversationComponent
	streamChunks []string
	shouldError  bool
}

func (m *mockStreamingConversationComponent) ConverseStream(ctx context.Context, req *conversation.ConversationRequest, streamFunc func(ctx context.Context, chunk []byte) error) (*conversation.ConversationResponse, error) {
	if m.shouldError {
		return nil, errors.New("mock streaming error")
	}

	// Simulate streaming by calling streamFunc for each chunk
	for _, chunk := range m.streamChunks {
		if err := streamFunc(ctx, []byte(chunk)); err != nil {
			return nil, err
		}
	}

	return m.response, nil
}

// Mock stream server for testing that implements grpc.ServerStream
type mockStreamServer struct {
	ctx      context.Context
	messages []*runtimev1pb.ConversationStreamResponse
	sendErr  error
}

func (m *mockStreamServer) Context() context.Context {
	return m.ctx
}

func (m *mockStreamServer) Send(resp *runtimev1pb.ConversationStreamResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.messages = append(m.messages, resp)
	return nil
}

// Required grpc.ServerStream methods
func (m *mockStreamServer) SetHeader(metadata.MD) error  { return nil }
func (m *mockStreamServer) SendHeader(metadata.MD) error { return nil }
func (m *mockStreamServer) SetTrailer(metadata.MD)       {}
func (m *mockStreamServer) SendMsg(interface{}) error    { return nil }
func (m *mockStreamServer) RecvMsg(interface{}) error    { return nil }

func newMockAPI() *Universal {
	compStore := compstore.New()

	// Add non-streaming component
	compStore.AddConversation(fakeConversationComponentName, &mockConversationComponent{
		response: &conversation.ConversationResponse{
			Outputs: []conversation.ConversationResult{
				{Result: "Hello, this is a test response from non-streaming component"},
			},
			ConversationContext: "test-context-123",
		},
	})

	// Add streaming component
	compStore.AddConversation(fakeStreamingComponentName, &mockStreamingConversationComponent{
		mockConversationComponent: &mockConversationComponent{
			response: &conversation.ConversationResponse{
				Outputs: []conversation.ConversationResult{
					{Result: "Complete response"},
				},
				ConversationContext: "test-context-456",
			},
		},
		streamChunks: []string{"Hello ", "streaming ", "world!"},
	})

	// Add echo component (which supports tool calling simulation)
	// The echo component can simulate tool calling behavior for testing
	compStore.AddConversation(fakeToolCallingComponentName, &mockConversationComponent{
		response: &conversation.ConversationResponse{
			Outputs: []conversation.ConversationResult{
				{Result: "Echo response - tool calling will be handled by the echo component logic"},
			},
			ConversationContext: "test-context-tools",
		},
	})

	return &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}
}

func TestConverseStreamAlpha1_ComponentValidation(t *testing.T) {
	testCases := []struct {
		name          string
		componentName string
		inputs        []*runtimev1pb.ConversationInput
		expectedError error
		setupEmpty    bool
	}{
		{
			name:          "No conversation components",
			componentName: fakeConversationComponentName,
			inputs:        []*runtimev1pb.ConversationInput{{Content: "test"}},
			expectedError: messages.ErrConversationNotFound,
			setupEmpty:    true,
		},
		{
			name:          "Component does not exist",
			componentName: "nonexistent",
			inputs:        []*runtimev1pb.ConversationInput{{Content: "test"}},
			expectedError: messages.ErrConversationNotFound.WithFormat("nonexistent"),
		},
		{
			name:          "No inputs provided",
			componentName: fakeConversationComponentName,
			inputs:        []*runtimev1pb.ConversationInput{},
			expectedError: messages.ErrConversationMissingInputs.WithFormat(fakeConversationComponentName),
		},
		{
			name:          "Valid request with non-streaming component",
			componentName: fakeConversationComponentName,
			inputs:        []*runtimev1pb.ConversationInput{{Content: "test"}},
		},
		{
			name:          "Valid request with streaming component",
			componentName: fakeStreamingComponentName,
			inputs:        []*runtimev1pb.ConversationInput{{Content: "test"}},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			var api *Universal
			if tt.setupEmpty {
				// Empty component store
				api = &Universal{
					logger:     testLogger,
					resiliency: resiliency.New(nil),
					compStore:  compstore.New(),
				}
			} else {
				api = newMockAPI()
			}

			req := &runtimev1pb.ConversationRequest{
				Name:   tt.componentName,
				Inputs: tt.inputs,
			}

			stream := &mockStreamServer{
				ctx: t.Context(),
			}

			err := api.ConverseStreamAlpha1(req, stream)

			if tt.expectedError != nil {
				require.ErrorIs(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				// Should have received some messages
				assert.NotEmpty(t, stream.messages, "Expected to receive stream messages")
			}
		})
	}
}

func TestConverseStreamAlpha1_NonStreamingComponent(t *testing.T) {
	api := newMockAPI()

	falseBool := false
	req := &runtimev1pb.ConversationRequest{
		Name: fakeConversationComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{Content: "Tell me a story"},
		},
		ScrubPII: &falseBool, // Disable PII scrubbing for simpler testing
	}

	stream := &mockStreamServer{
		ctx: t.Context(),
	}

	err := api.ConverseStreamAlpha1(req, stream)
	require.NoError(t, err)

	// Verify we got chunks + completion
	assert.Greater(t, len(stream.messages), 1, "Expected multiple messages (chunks + completion)")

	// Check that we have chunk messages
	hasChunks := false
	hasCompletion := false

	for _, msg := range stream.messages {
		if chunk := msg.GetChunk(); chunk != nil {
			hasChunks = true
			assert.NotEmpty(t, chunk.GetContent(), "Chunk content should not be empty")
		}
		if complete := msg.GetComplete(); complete != nil {
			hasCompletion = true
			assert.Equal(t, "test-context-123", complete.GetContextID())
		}
	}

	assert.True(t, hasChunks, "Expected to receive chunk messages")
	assert.True(t, hasCompletion, "Expected to receive completion message")
}

func TestConverseStreamAlpha1_StreamingComponent(t *testing.T) {
	api := newMockAPI()

	falseBool := false
	req := &runtimev1pb.ConversationRequest{
		Name: fakeStreamingComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{Content: "Hello streaming"},
		},
		ScrubPII: &falseBool, // Disable PII scrubbing for simpler testing
	}

	stream := &mockStreamServer{
		ctx: t.Context(),
	}

	err := api.ConverseStreamAlpha1(req, stream)
	require.NoError(t, err)

	// Verify we got the expected chunks
	chunkContents := []string{}
	hasCompletion := false

	for _, msg := range stream.messages {
		if chunk := msg.GetChunk(); chunk != nil {
			chunkContents = append(chunkContents, chunk.GetContent())
		}
		if complete := msg.GetComplete(); complete != nil {
			hasCompletion = true
			assert.Equal(t, "test-context-456", complete.GetContextID())
		}
	}

	// Should have received the streaming chunks
	expectedChunks := []string{"Hello ", "streaming ", "world!"}
	assert.Equal(t, expectedChunks, chunkContents, "Expected specific streaming chunks")
	assert.True(t, hasCompletion, "Expected completion message")
}

func TestConverseStreamAlpha1_ComponentError(t *testing.T) {
	compStore := compstore.New()

	// Add error component
	compStore.AddConversation("errorComponent", &mockConversationComponent{
		shouldError: true,
	})

	api := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	req := &runtimev1pb.ConversationRequest{
		Name: "errorComponent",
		Inputs: []*runtimev1pb.ConversationInput{
			{Content: "test"},
		},
	}

	stream := &mockStreamServer{
		ctx: t.Context(),
	}

	err := api.ConverseStreamAlpha1(req, stream)
	require.Error(t, err, "Expected gRPC error to be returned")
	assert.Contains(t, err.Error(), "mock conversation error")
}

func TestConverseStreamAlpha1_StreamError(t *testing.T) {
	api := newMockAPI()

	req := &runtimev1pb.ConversationRequest{
		Name: fakeConversationComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{Content: "test"},
		},
	}

	stream := &mockStreamServer{
		ctx:     t.Context(),
		sendErr: errors.New("stream send error"),
	}

	err := api.ConverseStreamAlpha1(req, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream send error")
}

func TestStreamingPIIScrubber(t *testing.T) {
	t.Run("PII scrubbing enabled with buffering", func(t *testing.T) {
		scrubber, err := NewStreamingPIIScrubber(5)
		require.NoError(t, err)

		// Send chunk smaller than window - should buffer
		result1 := scrubber.ProcessChunk([]byte("Hi"))
		assert.Nil(t, result1, "Should buffer small chunks")

		// Send more data - should process some
		result2 := scrubber.ProcessChunk([]byte(" there"))
		assert.NotNil(t, result2, "Should process data leaving window")

		// Flush remaining
		result3 := scrubber.Flush()
		assert.NotNil(t, result3, "Should return remaining buffer")
	})
}

func TestStreamingPipelineImpl(t *testing.T) {
	t.Run("Create pipeline success", func(t *testing.T) {
		pipeline := NewStreamingPipelineImpl(testLogger)
		assert.NotNil(t, pipeline)
		assert.Empty(t, pipeline.middleware, "No middleware should be registered initially")
	})

	t.Run("Add PII middleware", func(t *testing.T) {
		pipeline := NewStreamingPipelineImpl(testLogger)
		scrubberMiddleware, err := NewStreamingPIIScrubber(50)
		require.NoError(t, err)
		pipeline.AddMiddleware(scrubberMiddleware)
		assert.Len(t, pipeline.middleware, 1, "PII scrubber middleware should be registered")
	})

	t.Run("Add multiple middleware", func(t *testing.T) {
		pipeline := NewStreamingPipelineImpl(testLogger)

		// Add PII scrubber
		scrubberMiddleware, err := NewStreamingPIIScrubber(50)
		require.NoError(t, err)
		pipeline.AddMiddleware(scrubberMiddleware)

		// Add custom middleware (create a simple mock for testing)
		mockMiddleware := &simpleMockMiddleware{processedContent: "processed"}
		pipeline.AddMiddleware(mockMiddleware)

		assert.Len(t, pipeline.middleware, 2, "Should have both PII scrubber and custom middleware")
	})
}

func TestRequestProcessing(t *testing.T) {
	api := newMockAPI()

	t.Run("Process inputs with role", func(t *testing.T) {
		userRole := "user"
		assistantRole := "assistant"

		req := &runtimev1pb.ConversationRequest{
			Name: fakeConversationComponentName,
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Content: "Hello",
					Role:    &userRole,
				},
				{
					Content: "Hi there",
					Role:    &assistantRole,
				},
			},
		}

		stream := &mockStreamServer{
			ctx: t.Context(),
		}

		err := api.ConverseStreamAlpha1(req, stream)
		require.NoError(t, err)
	})

	t.Run("Process with temperature", func(t *testing.T) {
		temperature := 0.7
		req := &runtimev1pb.ConversationRequest{
			Name: fakeConversationComponentName,
			Inputs: []*runtimev1pb.ConversationInput{
				{Content: "test"},
			},
			Temperature: &temperature,
		}

		stream := &mockStreamServer{
			ctx: t.Context(),
		}

		err := api.ConverseStreamAlpha1(req, stream)
		require.NoError(t, err)
	})

	t.Run("Process with context ID", func(t *testing.T) {
		contextID := "existing-context"
		req := &runtimev1pb.ConversationRequest{
			Name: fakeConversationComponentName,
			Inputs: []*runtimev1pb.ConversationInput{
				{Content: "test"},
			},
			ContextID: &contextID,
		}

		stream := &mockStreamServer{
			ctx: t.Context(),
		}

		err := api.ConverseStreamAlpha1(req, stream)
		require.NoError(t, err)
	})
}

// NEW: Tool calling integration tests
func TestConversationToolCalling_BasicRequestHandling(t *testing.T) {
	api := newMockAPI()

	// Test basic tool calling request with tool definitions
	req := &runtimev1pb.ConversationRequest{
		Name: fakeToolCallingComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{
				Content: "What's the weather like in San Francisco?",
				Role:    func(s string) *string { return &s }("user"),
				Tools: []*runtimev1pb.Tool{
					{
						Type: "function",
						Function: &runtimev1pb.ToolFunction{
							Name:        "get_weather",
							Description: "Get current weather for a location",
							Parameters:  `{"type":"object","properties":{"location":{"type":"string","description":"City and state"}},"required":["location"]}`,
						},
					},
				},
			},
		},
	}

	resp, err := api.ConverseAlpha1(t.Context(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.GetOutputs(), 1)

	// Basic validation - the mock just echoes back, comprehensive tool calling
	// functionality is tested in integration tests with real echo component
	output := resp.GetOutputs()[0]
	assert.NotEmpty(t, output.GetResult())
	assert.Equal(t, "test-context-tools", resp.GetContextID())
}

func TestConversationToolCalling_ToolResultRequestHandling(t *testing.T) {
	api := newMockAPI()

	// Test tool result handling
	req := &runtimev1pb.ConversationRequest{
		Name: fakeToolCallingComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{
				Content:    `{"temperature": 72, "condition": "sunny"}`,
				Role:       func(s string) *string { return &s }("tool"),
				ToolCallId: func(s string) *string { return &s }("call_test_12345"),
				Name:       func(s string) *string { return &s }("get_weather"),
			},
		},
	}

	resp, err := api.ConverseAlpha1(t.Context(), req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.GetOutputs(), 1)

	// Basic validation - the mock just echoes back
	output := resp.GetOutputs()[0]
	assert.NotEmpty(t, output.GetResult())
}

func TestConversationToolCalling_ToolDefinitionConversion(t *testing.T) {
	// Test the tool definition conversion function
	protoTools := []*runtimev1pb.Tool{
		{
			Type: "function",
			Function: &runtimev1pb.ToolFunction{
				Name:        "test_function",
				Description: "A test function",
				Parameters:  `{"type":"object","properties":{"param1":{"type":"string"}},"required":["param1"]}`,
			},
		},
		{
			Type: "function",
			Function: &runtimev1pb.ToolFunction{
				Name:        "weather_function",
				Description: "Get weather for a location",
				Parameters:  `{"type":"object","properties":{"location":{"type":"string","description":"City name"},"units":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]}`,
			},
		},
	}

	// Test convertProtoToolsToComponentsContrib (the function we actually use)
	componentsContribTools := convertProtoToolsToComponentsContrib(protoTools)

	require.Len(t, componentsContribTools, 2)

	// Test first tool
	tool1 := componentsContribTools[0]
	assert.Equal(t, "function", tool1.Type)
	assert.Equal(t, "test_function", tool1.Function.Name)
	assert.Equal(t, "A test function", tool1.Function.Description)
	assert.Equal(t, `{"type":"object","properties":{"param1":{"type":"string"}},"required":["param1"]}`, tool1.Function.Parameters)

	// Test second tool
	tool2 := componentsContribTools[1]
	assert.Equal(t, "function", tool2.Type)
	assert.Equal(t, "weather_function", tool2.Function.Name)
	assert.Equal(t, "Get weather for a location", tool2.Function.Description)
	assert.Equal(t, `{"type":"object","properties":{"location":{"type":"string","description":"City name"},"units":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]}`, tool2.Function.Parameters)
}

func TestConversationToolCalling_ComponentsContribToolDefinitionConversion_EdgeCases(t *testing.T) {
	t.Run("empty tools array", func(t *testing.T) {
		result := convertProtoToolsToComponentsContrib([]*runtimev1pb.Tool{})
		assert.Nil(t, result)
	})

	t.Run("nil tools array", func(t *testing.T) {
		result := convertProtoToolsToComponentsContrib(nil)
		assert.Nil(t, result)
	})

	t.Run("tool with empty fields", func(t *testing.T) {
		protoTools := []*runtimev1pb.Tool{
			{
				Type: "",
				Function: &runtimev1pb.ToolFunction{
					Name:        "",
					Description: "",
					Parameters:  "",
				},
			},
		}

		result := convertProtoToolsToComponentsContrib(protoTools)
		require.Len(t, result, 1)

		tool := result[0]
		assert.Equal(t, "", tool.Type)
		assert.Equal(t, "", tool.Function.Name)
		assert.Equal(t, "", tool.Function.Description)
		assert.Equal(t, "", tool.Function.Parameters)
	})

	t.Run("tool with nil function", func(t *testing.T) {
		protoTools := []*runtimev1pb.Tool{
			{
				Type:     "function",
				Function: nil,
			},
		}

		// This should not panic and should handle the nil function gracefully
		result := convertProtoToolsToComponentsContrib(protoTools)
		require.Len(t, result, 1)

		tool := result[0]
		assert.Equal(t, "function", tool.Type)
		assert.Equal(t, "", tool.Function.Name)
		assert.Equal(t, "", tool.Function.Description)
		assert.Equal(t, "", tool.Function.Parameters)
	})
}

func TestConversationToolCalling_StreamingWithTools(t *testing.T) {
	api := newMockAPI()

	req := &runtimev1pb.ConversationRequest{
		Name: fakeToolCallingComponentName,
		Inputs: []*runtimev1pb.ConversationInput{
			{
				Content: "What's the weather like in New York?",
				Role:    func(s string) *string { return &s }("user"),
				Tools: []*runtimev1pb.Tool{
					{
						Type: "function",
						Function: &runtimev1pb.ToolFunction{
							Name:        "get_weather",
							Description: "Get current weather for a location",
							Parameters:  `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`,
						},
					},
				},
			},
		},
	}

	stream := &mockStreamServer{
		ctx: t.Context(),
	}

	err := api.ConverseStreamAlpha1(req, stream)

	require.NoError(t, err)
	assert.NotEmpty(t, stream.messages, "Expected streaming messages")

	// Should have received content chunks and completion
	hasContent := false
	hasCompletion := false

	for _, msg := range stream.messages {
		if chunk := msg.GetChunk(); chunk != nil {
			hasContent = true
		}
		if complete := msg.GetComplete(); complete != nil {
			hasCompletion = true
		}
	}

	assert.True(t, hasContent, "Expected content chunks")
	assert.True(t, hasCompletion, "Expected completion message")
}
