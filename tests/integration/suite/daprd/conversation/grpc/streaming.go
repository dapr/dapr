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

package http

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(streaming))
}

type streaming struct {
	daprd *daprd.Daprd
}

func (s *streaming) Setup(t *testing.T) []framework.Option {
	s.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey
`))

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *streaming) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	t.Run("basic streaming", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Message: "Hello streaming world"},
			},
		})
		require.NoError(t, err)

		// Collect all streaming responses
		var chunks []string
		var hasCompletion bool
		var contextID string

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
				contextID = complete.GetContextID()
			}
			if errMsg := resp.GetError(); errMsg != nil {
				t.Fatalf("Unexpected error in stream: %s", errMsg.GetMessage())
			}
		}

		// Verify streaming behavior
		assert.NotEmpty(t, chunks, "Should receive streaming chunks")
		assert.True(t, hasCompletion, "Should receive completion message")

		// Verify full response content
		fullResponse := strings.Join(chunks, "")
		assert.Equal(t, "Hello streaming world", fullResponse)

		// Verify we got multiple chunks (not just one complete response)
		assert.Greater(t, len(chunks), 1, "Should receive multiple chunks in streaming mode")

		// Verify context ID is provided
		assert.NotEmpty(t, contextID, "Should receive context ID in completion")
	})

	t.Run("streaming with PII scrubbing", func(t *testing.T) {
		scrubPII := true
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Message: "My phone number is +1234567890",
				},
			},
			ScrubPII: &scrubPII,
		})
		require.NoError(t, err)

		var chunks []string
		var hasCompletion bool

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.True(t, hasCompletion, "Should receive completion")

		// Verify PII was scrubbed in streaming chunks
		fullResponse := strings.Join(chunks, "")
		assert.Contains(t, fullResponse, "<PHONE_NUMBER>", "Phone number should be scrubbed")
		assert.NotContains(t, fullResponse, "+1234567890", "Original phone number should not appear")
	})

	t.Run("streaming with multiple inputs", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Message: "First message"},
				{Message: "Second message"},
			},
		})
		require.NoError(t, err)

		var chunks []string
		var hasCompletion bool

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.True(t, hasCompletion, "Should receive completion")

		// Both messages should be present in the response
		fullResponse := strings.Join(chunks, "")
		assert.Contains(t, fullResponse, "First message")
		assert.Contains(t, fullResponse, "Second message")
	})

	t.Run("streaming error handling - nonexistent component", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "nonexistent",
			Inputs: []*rtv1.ConversationInput{
				{Message: "test"},
			},
		})
		// The stream creation might succeed, but we should get an error when receiving
		if err != nil {
			// Error during stream creation is acceptable
			assert.Contains(t, err.Error(), "not found")
			return
		}

		// If stream creation succeeded, we should get an error message in the stream
		resp, err := stream.Recv()
		if err != nil {
			// gRPC error is acceptable
			assert.Contains(t, err.Error(), "not found")
		} else if errMsg := resp.GetError(); errMsg != nil {
			// Error message in stream is also acceptable
			assert.Contains(t, errMsg.GetMessage(), "not found")
		} else {
			t.Fatal("Expected error for nonexistent component")
		}
	})

	t.Run("streaming error handling - empty inputs", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name:   "echo",
			Inputs: []*rtv1.ConversationInput{}, // Empty inputs
		})
		// Similar to above - error can be during creation or in stream
		if err != nil {
			assert.Contains(t, err.Error(), "input")
			return
		}

		resp, err := stream.Recv()
		if err != nil {
			assert.Contains(t, err.Error(), "input")
		} else if errMsg := resp.GetError(); errMsg != nil {
			assert.Contains(t, errMsg.GetMessage(), "input")
		} else {
			t.Fatal("Expected error for empty inputs")
		}
	})

	t.Run("streaming with temperature parameter", func(t *testing.T) {
		temperature := 0.7
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Message: "Hello with temperature"},
			},
			Temperature: &temperature,
		})
		require.NoError(t, err)

		var chunks []string
		var hasCompletion bool

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.True(t, hasCompletion, "Should receive completion")

		fullResponse := strings.Join(chunks, "")
		assert.Equal(t, "Hello with temperature", fullResponse)
	})

	t.Run("streaming with context ID", func(t *testing.T) {
		contextID := "test-context-123"
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Message: "Hello with context"},
			},
			ContextID: &contextID,
		})
		require.NoError(t, err)

		var chunks []string
		var hasCompletion bool
		var returnedContextID string

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
				returnedContextID = complete.GetContextID()
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.True(t, hasCompletion, "Should receive completion")
		assert.Equal(t, contextID, returnedContextID, "Should return the provided context ID")

		fullResponse := strings.Join(chunks, "")
		assert.Equal(t, "Hello with context", fullResponse)
	})

	t.Run("streaming chunk boundaries", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Message: "This is a longer message that should be split into multiple chunks for streaming"},
			},
		})
		require.NoError(t, err)

		var chunks []string
		var tokenBoundaries []bool

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
				if chunk.TokenBoundary != nil {
					tokenBoundaries = append(tokenBoundaries, chunk.GetTokenBoundary())
				}
			}
			if resp.GetComplete() != nil {
				break
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.Greater(t, len(chunks), 1, "Should receive multiple chunks for longer message")

		// Verify token boundary information is provided
		assert.NotEmpty(t, tokenBoundaries, "Should provide token boundary information")

		// Full response should be complete
		fullResponse := strings.Join(chunks, "")
		assert.Equal(t, "This is a longer message that should be split into multiple chunks for streaming", fullResponse)
	})

	t.Run("streaming with role-based inputs", func(t *testing.T) {
		userRole := "user"
		assistantRole := "assistant"

		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Message: "Hello assistant",
					Role:    &userRole,
				},
				{
					Message: "Hello user",
					Role:    &assistantRole,
				},
			},
		})
		require.NoError(t, err)

		var chunks []string
		var hasCompletion bool

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			if chunk := resp.GetChunk(); chunk != nil {
				chunks = append(chunks, chunk.GetContent())
			}
			if complete := resp.GetComplete(); complete != nil {
				hasCompletion = true
			}
		}

		assert.NotEmpty(t, chunks, "Should receive chunks")
		assert.True(t, hasCompletion, "Should receive completion")

		fullResponse := strings.Join(chunks, "")
		assert.Contains(t, fullResponse, "Hello assistant")
		assert.Contains(t, fullResponse, "Hello user")
	})
}
