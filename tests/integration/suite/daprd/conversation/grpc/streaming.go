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

package grpc

import (
	"context"
	"io"
	"os"
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

type liveConversationAIProvider struct {
	componentName string
	envVar        string
}

var liveConversationAIProviders = []liveConversationAIProvider{
	{
		componentName: "openai",
		envVar:        "OPENAI_API_KEY",
	},
	{
		componentName: "anthropic",
		envVar:        "ANTHROPIC_API_KEY",
	},
	{
		componentName: "googleai",
		envVar:        "GOOGLE_API_KEY",
	},
}

func (s *streaming) Setup(t *testing.T) []framework.Option {
	// Build component configuration - always include echo, conditionally include AI providers
	componentConfig := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey`

	// Define AI provider components

	// Add AI provider components if their API keys are available
	for _, provider := range liveConversationAIProviders {
		if apiKey := os.Getenv(provider.envVar); apiKey != "" {
			componentConfig += `
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: ` + provider.componentName + `
spec:
  type: conversation.` + provider.componentName + `
  version: v1
  metadata:
  - name: key
    value: ` + apiKey
		}
	}

	s.daprd = daprd.New(t, daprd.WithResourceFiles(componentConfig))

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
				{Content: "Hello streaming world"},
			},
		})
		require.NoError(t, err)

		// Collect all streaming responses
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

		// Verify streaming behavior
		assert.NotEmpty(t, chunks, "Should receive streaming chunks")
		assert.True(t, hasCompletion, "Should receive completion message")

		// Verify full response content
		fullResponse := strings.Join(chunks, "")
		assert.Equal(t, "Hello streaming world", fullResponse)

		// Verify we got multiple chunks (not just one complete response)
		assert.Greater(t, len(chunks), 1, "Should receive multiple chunks in streaming mode")

		// Note: Echo component may or may not provide context ID (test component behavior)
	})

	t.Run("streaming with PII scrubbing", func(t *testing.T) {
		scrubPII := true
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "My phone number is +1234567890",
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
		// Test streaming with concatenated input (since echo only returns last message)
		combinedInput := "First message Second message"
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Content: combinedInput},
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
				{Content: "test"},
			},
		})
		// Should get a gRPC error either during stream creation or first receive
		if err != nil {
			// Error during stream creation
			assert.Contains(t, err.Error(), "failed finding conversation component")
			return
		}

		// If stream creation succeeded, we should get a gRPC error on first receive
		_, err = stream.Recv()
		require.Error(t, err, "Expected gRPC error for nonexistent component")
		assert.Contains(t, err.Error(), "failed finding conversation component")
	})

	t.Run("streaming error handling - empty inputs", func(t *testing.T) {
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name:   "echo",
			Inputs: []*rtv1.ConversationInput{}, // Empty inputs
		})
		// Should get a gRPC error either during stream creation or first receive
		if err != nil {
			assert.Contains(t, err.Error(), "input")
			return
		}

		// If stream creation succeeded, we should get a gRPC error on first receive
		_, err = stream.Recv()
		require.Error(t, err, "Expected gRPC error for empty inputs")
		assert.Contains(t, err.Error(), "input")
	})

	t.Run("streaming with temperature parameter", func(t *testing.T) {
		temperature := 0.7
		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{Content: "Hello with temperature"},
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
				{Content: "Hello with context"},
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

	t.Run("streaming with role-based inputs", func(t *testing.T) {
		userRole := "user"
		// Test streaming with concatenated input (since echo only returns last message)
		combinedInput := "Hello assistant Hello user"

		stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: combinedInput,
					Role:    &userRole,
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

	// Live tests with real AI providers - only runs if API keys are available
	// To run these tests with real API keys:
	// OPENAI_API_KEY=your-actual-key ANTHROPIC_API_KEY=your-actual-key GOOGLE_API_KEY=your-actual-key CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/streaming"
	providersExpectedText := map[string]string{
		"openai":    "Hello from OpenAI streaming!",
		"anthropic": "Hello from Anthropic streaming!",
		"googleai":  "Hello from Google Gemini streaming!",
	}

	for _, provider := range liveConversationAIProviders {
		t.Run("streaming with "+provider.componentName+" live", func(t *testing.T) {
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping live %s test", provider.envVar, provider.componentName)
			}

			stream, err := client.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
				Name: provider.componentName,
				Inputs: []*rtv1.ConversationInput{
					{Content: "Say '" + providersExpectedText[provider.componentName] + "' and nothing else."},
				},
			})
			require.NoError(t, err)

			var chunks []string
			var hasCompletion bool
			var usage *rtv1.ConversationUsage

			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)

				if chunk := resp.GetChunk(); chunk != nil {
					chunks = append(chunks, chunk.GetContent())
					t.Logf("Received chunk: %q", chunk.GetContent())
				}
				if complete := resp.GetComplete(); complete != nil {
					hasCompletion = true
					usage = complete.GetUsage()
					t.Logf("Stream completed with usage: %+v", usage)
				}
			}

			require.NotEmpty(t, chunks, "Should receive chunks from "+provider.componentName)
			assert.True(t, hasCompletion, "Should receive completion")
			require.NotEmpty(t, chunks, "Should receive at least one chunk")

			// Verify we got a real response from the provider
			fullResponse := strings.Join(chunks, "")
			assert.NotEmpty(t, fullResponse, "Should receive non-empty response from "+provider.componentName)
			t.Logf("Full %s response: %q", provider.componentName, fullResponse)

			// Verify response contains the expected content
			assert.Contains(t, fullResponse, providersExpectedText[provider.componentName])

			// Verify usage information is provided
			if usage != nil {
				assert.Positive(t, usage.GetTotalTokens(), "Should have token usage information")
				t.Logf("Token usage - Prompt: %d, Completion: %d, Total: %d",
					usage.GetPromptTokens(), usage.GetCompletionTokens(), usage.GetTotalTokens())
			}
		})
	}
}
