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
				ctx: context.Background(),
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
		ctx: context.Background(),
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
			assert.Equal(t, "test-context-123", complete.GetContextId())
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
		ctx: context.Background(),
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
			assert.Equal(t, "test-context-456", complete.GetContextId())
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
		ctx: context.Background(),
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
		ctx:     context.Background(),
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
			ctx: context.Background(),
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
			ctx: context.Background(),
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
			ContextId: &contextID,
		}

		stream := &mockStreamServer{
			ctx: context.Background(),
		}

		err := api.ConverseStreamAlpha1(req, stream)
		require.NoError(t, err)
	})
}
