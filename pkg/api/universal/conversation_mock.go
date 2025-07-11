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

	contribConverse "github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/api/grpc/metadata"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"
)

const (
	fakeConversationComponentName = "fakeConversationComponent"
	fakeStreamingComponentName    = "fakeStreamingComponent"
	fakeToolCallingComponentName  = "fakeToolCallingComponent"
)

type mockConversationComponent struct {
	shouldError bool
	response    *contribConverse.ConversationResponse
}

func (m *mockConversationComponent) Init(ctx context.Context, meta contribConverse.Metadata) error {
	return nil
}

func (m *mockConversationComponent) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func (m *mockConversationComponent) Converse(ctx context.Context, req *contribConverse.ConversationRequest) (*contribConverse.ConversationResponse, error) {
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

func (m *mockStreamingConversationComponent) ConverseStream(ctx context.Context, req *contribConverse.ConversationRequest, streamFunc func(ctx context.Context, chunk []byte) error) (*contribConverse.ConversationResponse, error) {
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
		response: &contribConverse.ConversationResponse{
			Outputs: []contribConverse.ConversationOutput{
				{
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "Hello, this is a test response from non-streaming component"},
					},
				},
			},
			Context: "test-context-123",
		},
	})

	// Add streaming component
	compStore.AddConversation(fakeStreamingComponentName, &mockStreamingConversationComponent{
		mockConversationComponent: &mockConversationComponent{
			response: &contribConverse.ConversationResponse{
				Outputs: []contribConverse.ConversationOutput{
					{
						Parts: []contribConverse.ContentPart{
							contribConverse.TextContentPart{Text: "Complete response"},
						},
					},
				},
				Context: "test-context-456",
			},
		},
		streamChunks: []string{"Hello ", "streaming ", "world!"},
	})

	// Add echo component (which supports tool calling simulation)
	// The echo component can simulate tool calling behavior for testing
	compStore.AddConversation(fakeToolCallingComponentName, &mockConversationComponent{
		response: &contribConverse.ConversationResponse{
			Outputs: []contribConverse.ConversationOutput{
				{
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "Echo response - tool calling will be handled by the echo component logic"},
					},
				},
			},
			Context: "test-context-tools",
		},
	})

	return &Universal{
		logger:     logger.NewLogger("testlogger"),
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}
}
