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

package grpc

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(toolCallingDebug))
}

type toolCallingDebug struct {
	daprd *daprd.Daprd
}

func (td *toolCallingDebug) Setup(t *testing.T) []framework.Option {
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
  - name: model
    value: "echo-model"`

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

	td.daprd = daprd.New(t, daprd.WithResourceFiles(componentConfig))

	return []framework.Option{
		framework.WithProcesses(td.daprd),
	}
}

func (td *toolCallingDebug) Run(t *testing.T, ctx context.Context) {
	td.daprd.WaitUntilRunning(t, ctx)

	client := td.daprd.GRPCClient(t, ctx)

	t.Run("echo tool calling", func(t *testing.T) {
		// Test what the echo component returns when we send tools
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
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
								Parameters:  `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`,
							},
						},
					},
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Outputs, 1)

		output := resp.Outputs[0]
		t.Logf("Echo Response - Result: %s", output.Result)
		t.Logf("Echo Response - ToolCalls: %v", output.ToolCalls)
		t.Logf("Echo Response - FinishReason: %s", output.GetFinishReason())

		// Echo should recognize the weather keywords and suggest tool calling
		assert.Contains(t, output.Result, "tools")
		// Expect tool calls to be generated for weather-related queries
		assert.NotEmpty(t, output.ToolCalls, "Echo should generate tool calls for weather queries")
		assert.Equal(t, "tool_calls", output.GetFinishReason())
	})

	t.Run("echo normal conversation", func(t *testing.T) {
		// Test normal echo behavior for comparison
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Content: "Hello world",
					Role:    func(s string) *string { return &s }("user"),
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Outputs, 1)

		output := resp.Outputs[0]
		t.Logf("Echo Normal Response - Result: %s", output.Result)
		t.Logf("Echo Normal Response - ToolCalls: %v", output.ToolCalls)

		assert.Equal(t, "Hello world", output.Result)
		assert.Empty(t, output.ToolCalls)
	})

	// Test with real AI providers if API keys are available
	// To run these tests with real API keys:
	// OPENAI_API_KEY=your-key ANTHROPIC_API_KEY=your-key GOOGLE_API_KEY=your-key CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/toolCallingDebug"
	for _, provider := range liveConversationAIProviders {
		t.Run("tool calling with "+provider.componentName+" live", func(t *testing.T) {
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping live %s test", provider.envVar, provider.componentName)
			}

			req := &runtimev1pb.ConversationRequest{
				Name: provider.componentName,
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Content: "What's the weather like in San Francisco? Please use the get_weather function.",
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

			resp, err := client.ConverseAlpha1(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Greater(t, len(resp.Outputs), 0, "Should have at least one output")

			// Log all outputs for debugging
			for i, output := range resp.Outputs {
				t.Logf("%s Output[%d] - Result: %s", provider.componentName, i, output.Result)
				t.Logf("%s Output[%d] - ToolCalls: %v", provider.componentName, i, output.ToolCalls)
				t.Logf("%s Output[%d] - FinishReason: %s", provider.componentName, i, output.GetFinishReason())
			}

			// Provider-specific tool calling validation (following components-contrib conformance pattern)
			switch provider.componentName {
			case "anthropic":
				// Anthropic may return multiple outputs: explanation + tool calls
				var toolCallOutput *runtimev1pb.ConversationResult
				for _, output := range resp.Outputs {
					if len(output.ToolCalls) > 0 {
						toolCallOutput = output
						break
					}
				}

				if toolCallOutput != nil {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					assert.Equal(t, "tool_calls", toolCallOutput.GetFinishReason())
					toolCall := toolCallOutput.ToolCalls[0]
					assert.Equal(t, "get_weather", toolCall.Function.Name)
					assert.NotEmpty(t, toolCall.Id)
					// Note: Anthropic may not populate Type field consistently via LangChain Go
				} else {
					t.Logf("ℹ️ %s chose not to call tools for this request (acceptable)", provider.componentName)
				}

			case "googleai":
				// GoogleAI through LangChain Go doesn't populate Type and ID fields consistently
				require.Len(t, resp.Outputs, 1)
				output := resp.Outputs[0]

				if len(output.ToolCalls) > 0 {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					assert.Equal(t, "tool_calls", output.GetFinishReason())
					toolCall := output.ToolCalls[0]
					assert.Equal(t, "get_weather", toolCall.Function.Name)
					// Skip Type and ID assertions for GoogleAI due to LangChain Go implementation differences
				} else {
					t.Logf("ℹ️ %s chose not to call tools for this request", provider.componentName)
				}

			default:
				// Standard validation for other providers (OpenAI, etc.)
				require.Len(t, resp.Outputs, 1)
				output := resp.Outputs[0]

				if len(output.ToolCalls) > 0 {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					toolCall := output.ToolCalls[0]
					assert.Equal(t, "function", toolCall.Type)
					assert.Equal(t, "get_weather", toolCall.Function.Name)
					assert.NotEmpty(t, toolCall.Id)
					assert.Equal(t, "tool_calls", output.GetFinishReason())
				} else {
					t.Logf("ℹ️ %s didn't return tool calls - this might be expected depending on the model behavior", provider.componentName)
				}
			}
		})

		t.Run("streaming tool calling with "+provider.componentName+" live", func(t *testing.T) {
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping streaming %s test", provider.envVar, provider.componentName)
			}

			// Check for known streaming issues with specific providers
			switch provider.componentName {
			case "anthropic":
				t.Skipf("Anthropic has known streaming issues with tool calling (components-contrib limitation)")
				return
			case "googleai":
				// GoogleAI streaming might have different behavior
			}

			req := &runtimev1pb.ConversationRequest{
				Name: provider.componentName,
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Content: "Please call get_weather for New York City.",
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

			stream, err := client.ConverseStreamAlpha1(ctx, req)
			if err != nil {
				// Check if streaming is disabled for this component
				if strings.Contains(err.Error(), "streaming is not supported") ||
					strings.Contains(err.Error(), "streaming not supported") {
					t.Skipf("Component %s has streaming disabled, skipping streaming tool calling test", provider.componentName)
					return
				}
				require.NoError(t, err, "Failed to create stream")
			}

			var chunks []string
			var toolCalls []*runtimev1pb.ToolCall
			var finishReason string

			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					// Handle provider-specific streaming errors
					if strings.Contains(err.Error(), "invalid delta text field type") {
						t.Skipf("Component %s has streaming implementation issues: %v", provider.componentName, err)
						return
					}
					require.NoError(t, err)
				}

				if chunk := resp.GetChunk(); chunk != nil {
					if chunk.Content != "" {
						chunks = append(chunks, chunk.Content)
						t.Logf("Received chunk: %s", chunk.Content)
					}
					if len(chunk.ToolCalls) > 0 {
						toolCalls = append(toolCalls, chunk.ToolCalls...)
						t.Logf("Received tool calls: %v", chunk.ToolCalls)
					}
					if chunk.FinishReason != nil {
						finishReason = *chunk.FinishReason
						t.Logf("Finish reason: %s", finishReason)
					}
				}
			}

			t.Logf("Streaming complete - Chunks: %d, ToolCalls: %d, FinishReason: %s",
				len(chunks), len(toolCalls), finishReason)

			// Provider-specific streaming tool call validation
			if len(toolCalls) > 0 {
				t.Logf("✅ Streaming tool calling working with %s!", provider.componentName)

				toolCall := toolCalls[0]
				assert.Equal(t, "get_weather", toolCall.Function.Name)
				assert.Contains(t, strings.ToLower(toolCall.Function.Arguments), "new york")

				// Provider-specific Type and ID field validation
				switch provider.componentName {
				case "googleai":
					// Skip Type and ID assertions for GoogleAI due to LangChain Go implementation differences
				default:
					assert.Equal(t, "function", toolCall.Type)
					assert.NotEmpty(t, toolCall.Id)
				}
			} else {
				t.Logf("ℹ️ %s didn't return tool calls in streaming mode - this might be expected", provider.componentName)
			}
		})
	}
}
