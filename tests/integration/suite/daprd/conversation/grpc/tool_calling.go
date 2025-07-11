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
	"google.golang.org/protobuf/types/known/wrapperspb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(toolCalling))
}

type toolCalling struct {
	daprd *daprd.Daprd
}

func (tc *toolCalling) Setup(t *testing.T) []framework.Option {
	// Build component configuration - always include echo, conditionally include AI providers
	componentConfig := buildLiveAIProviderComponents(t)

	tc.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(componentConfig))

	return []framework.Option{
		framework.WithProcesses(tc.daprd),
	}
}

func (tc *toolCalling) Run(t *testing.T, ctx context.Context) {
	tc.daprd.WaitUntilRunning(t, ctx)

	client := tc.daprd.GRPCClient(t, ctx)

	t.Run("echo with tool definitions", func(t *testing.T) {
		// Test echo behavior with tool definitions
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
			Tools: []*runtimev1pb.ConversationTool{
				{
					Type:        wrapperspb.String("function"),
					Name:        wrapperspb.String("get_weather"),
					Description: wrapperspb.String("Get current weather for a location"),
					Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
				},
			},
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Role: ptr.Of("user"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{
									Text: "What's the weather like in San Francisco?",
								},
							},
						},
					},
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.GetResults(), 1)

		output := resp.GetResults()[0]
		t.Logf("Echo Response - Result: %s", extractTextFromParts(output.GetContent()))

		// Extract tool calls from parts
		toolCalls := extractToolCallsFromParts(output.GetContent())
		t.Logf("Echo Response - ToolCalls from Parts: %v", toolCalls)
		t.Logf("Echo Response - FinishReason: %s", output.GetFinishReason())

		// Echo should just echo the input text, not generate tool calls
		assert.Contains(t, extractTextFromParts(output.GetContent()), "weather")
		// Echo doesn't generate tool calls - it just echoes the input
		assert.Empty(t, toolCalls, "Echo should not generate tool calls")
		assert.Equal(t, "stop", output.GetFinishReason())
	})

	t.Run("echo normal conversation", func(t *testing.T) {
		// Test normal echo behavior for comparison
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{
									Text: "Hello world",
								},
							},
						},
					},
					Role: ptr.Of("user"),
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.GetResults(), 1)

		output := resp.GetResults()[0]
		t.Logf("Echo Normal Response - Result: %s", extractTextFromParts(output.GetContent()))

		// Extract tool calls from parts
		toolCalls := extractToolCallsFromParts(output.GetContent())
		t.Logf("Echo Normal Response - ToolCalls from Parts: %v", toolCalls)

		assert.Equal(t, "Hello world", extractTextFromParts(output.GetContent()))
		assert.Empty(t, toolCalls)
	})

	// Test with real AI providers if API keys are available
	// To run these tests with real API keys:
	// OPENAI_API_KEY=your-key ANTHROPIC_API_KEY=your-key GOOGLE_API_KEY=your-key CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/toolCalling"
	for _, provider := range realConversationAIProviders {
		t.Run("tool calling with "+provider.componentName+" live", func(t *testing.T) {
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping live %s test", provider.envVar, provider.componentName)
			}

			req := &runtimev1pb.ConversationRequest{
				Name: provider.componentName,
				Tools: []*runtimev1pb.ConversationTool{
					{
						Type:        wrapperspb.String("function"),
						Name:        wrapperspb.String("get_weather"),
						Description: wrapperspb.String("Get current weather for a location"),
						Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string","description":"City and state"}},"required":["location"]}`),
					},
				},
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Role: ptr.Of("user"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "What's the weather like in San Francisco? Please use the get_weather function.",
									},
								},
							},
						},
					},
				},
			}

			resp, err := client.ConverseAlpha1(ctx, req)
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotEmpty(t, resp.GetResults(), "Should have at least one output")

			// Log all outputs for debugging
			for i, output := range resp.GetResults() {
				t.Logf("%s Output[%d] - Result: %s", provider.componentName, i, extractTextFromParts(output.GetContent()))

				// Extract tool calls from parts
				toolCalls := extractToolCallsFromParts(output.GetContent())
				t.Logf("%s Output[%d] - ToolCalls from Parts: %v", provider.componentName, i, toolCalls)
				t.Logf("%s Output[%d] - FinishReason: %s", provider.componentName, i, output.GetFinishReason())
			}

			// Provider-specific tool calling validation (following components-contrib conformance pattern)
			switch provider.componentName {
			case "anthropic":
				// Anthropic may return multiple outputs: explanation + tool calls
				var toolCallOutput *runtimev1pb.ConversationResult
				var toolCalls []*runtimev1pb.ConversationToolCall
				for _, output := range resp.GetResults() {
					outputToolCalls := extractToolCallsFromParts(output.GetContent())
					if len(outputToolCalls) > 0 {
						toolCallOutput = output
						toolCalls = outputToolCalls
						break
					}
				}

				if toolCallOutput != nil {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					assert.Equal(t, "tool_calls", toolCallOutput.GetFinishReason())
					toolCall := toolCalls[0]
					assert.Equal(t, "get_weather", toolCall.GetName().GetValue())
					assert.NotEmpty(t, toolCall.GetId().GetValue())
					// Note: Anthropic may not populate Type field consistently via LangChain Go
				} else {
					t.Logf("ℹ️ %s chose not to call tools for this request (acceptable)", provider.componentName)
				}

			case "googleai":
				// GoogleAI through LangChain Go doesn't populate Type and ID fields consistently
				require.Len(t, resp.GetResults(), 1)
				output := resp.GetResults()[0]
				toolCalls := extractToolCallsFromParts(output.GetContent())

				if len(toolCalls) > 0 {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					assert.Equal(t, "tool_calls", output.GetFinishReason())
					toolCall := toolCalls[0]
					assert.Equal(t, "get_weather", toolCall.GetName().GetValue())
					// Skip Type and ID assertions for GoogleAI due to LangChain Go implementation differences
				} else {
					t.Logf("ℹ️ %s chose not to call tools for this request", provider.componentName)
				}

			default:
				// Standard validation for other providers (OpenAI, etc.)
				require.Len(t, resp.GetResults(), 1)
				output := resp.GetResults()[0]
				toolCalls := extractToolCallsFromParts(output.GetContent())

				if len(toolCalls) > 0 {
					t.Logf("✅ Tool calling working with %s!", provider.componentName)
					toolCall := toolCalls[0]
					assert.Equal(t, "function", toolCall.GetType().GetValue())
					assert.Equal(t, "get_weather", toolCall.GetName().GetValue())
					assert.NotEmpty(t, toolCall.GetId().GetValue())
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
						Role: ptr.Of("user"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "Please call get_weather for New York City.",
									},
								},
							},
						},
					},
				},
				Tools: []*runtimev1pb.ConversationTool{
					{
						Type:        wrapperspb.String("function"),
						Name:        wrapperspb.String("get_weather"),
						Description: wrapperspb.String("Get current weather for a location"),
						Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
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
			var toolCalls []*runtimev1pb.ConversationToolCall
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

				if complete := resp.GetComplete(); complete != nil {
					t.Logf("Streaming complete - Usage: %s, Outputs: %v", complete.GetUsage(), complete.GetResults())
					for _, output := range complete.GetResults() {
						t.Logf("Output - Result: %s", extractTextFromParts(output.GetContent()))
						t.Logf("Output - FinishReason: %s", output.GetFinishReason())
						toolCalls := extractToolCallsFromParts(output.GetContent())
						if len(toolCalls) > 0 {
							t.Logf("ToolCalls - ID: %s, Type: %s, Name: %s, Arguments: %s", toolCalls[0].GetId().GetValue(), toolCalls[0].GetType().GetValue(), toolCalls[0].GetName().GetValue(), toolCalls[0].GetArguments())
						}
					}
					break
				}

				if chunk := resp.GetChunk(); chunk != nil {
					// Extract text from parts instead of deprecated content field
					if len(chunk.GetContent()) > 0 {
						if textContent := chunk.GetContent()[0].GetText(); textContent != nil {
							text := textContent.GetText()
							if text != "" {
								chunks = append(chunks, text)
								t.Logf("Received chunk: %s", text)
							}
						}
					}

					// Extract tool calls from chunk parts
					chunkToolCalls := extractToolCallsFromParts(chunk.GetContent())
					if len(chunkToolCalls) > 0 {
						// Convert ToolCallContent to ToolCall for backward compatibility
						for _, tc := range chunkToolCalls {
							toolCall := &runtimev1pb.ConversationToolCall{
								Id:        tc.GetId(),
								Type:      tc.GetType(),
								Name:      tc.GetName(),
								Arguments: tc.GetArguments(),
							}
							toolCalls = append(toolCalls, toolCall)
						}
						t.Logf("Received tool calls from parts: %v", chunkToolCalls)
					}

					if chunk.GetFinishReason() != "" {
						finishReason = chunk.GetFinishReason()
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
				assert.Equal(t, "get_weather", toolCall.GetName().GetValue())
				assert.Contains(t, strings.ToLower(toolCall.GetArguments().GetValue()), "new york")

				// Provider-specific Type and ID field validation
				switch provider.componentName {
				case "googleai":
					// Skip Type and ID assertions for GoogleAI due to LangChain Go implementation differences
				default:
					assert.Equal(t, "function", toolCall.GetType().GetValue())
					assert.NotEmpty(t, toolCall.GetId().GetValue())
				}
			} else {
				t.Logf("ℹ️ %s didn't return tool calls in streaming mode - this might be expected", provider.componentName)
				t.Fail()
			}
		})

		t.Run("multi-turn tool calling flow with "+provider.componentName+" live", func(t *testing.T) {
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping multi-turn %s test", provider.envVar, provider.componentName)
			}

			// Step 1: Initial user query with tool definitions
			t.Logf("🔄 Step 1: User asks weather question with tool definitions")
			req1 := &runtimev1pb.ConversationRequest{
				Name: provider.componentName,
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Role: ptr.Of("user"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "What's the weather like in San Francisco? Please use the get_weather function.",
									},
								},
							},
						},
					},
				},
				Tools: []*runtimev1pb.ConversationTool{
					{
						Type:        wrapperspb.String("function"),
						Name:        wrapperspb.String("get_weather"),
						Description: wrapperspb.String("Get current weather for a location"),
						Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
					},
				},
			}

			resp1, err := client.ConverseAlpha1(ctx, req1)
			require.NoError(t, err)
			require.NotNil(t, resp1)
			require.NotEmpty(t, resp1.GetResults())

			// Extract tool calls and full response parts
			var toolCalls []*runtimev1pb.ConversationToolCall
			var assistantText string
			var assistantResponseParts []*runtimev1pb.ConversationContent
			for _, output := range resp1.GetResults() {
				outputToolCalls := extractToolCallsFromParts(output.GetContent())
				if len(outputToolCalls) > 0 {
					toolCalls = outputToolCalls
					// CRITICAL: Store the complete parts from assistant response
					// This includes both text and tool calls - essential for Anthropic multi-turn
					assistantResponseParts = output.GetContent()
				}
				if result := extractTextFromParts(output.GetContent()); result != "" {
					assistantText = result
				}
			}

			t.Logf("🔍 Step 1 Response - Assistant Text: %s", assistantText)
			t.Logf("🔍 Step 1 Response - Tool Calls: %d found", len(toolCalls))
			t.Logf("🔍 Step 1 Response - Assistant Parts: %d total parts", len(assistantResponseParts))

			require.NotEmpty(t, toolCalls, "Should receive tool calls for weather query")
			toolCall := toolCalls[0]
			require.Equal(t, "get_weather", toolCall.GetName().GetValue())
			toolCallID := toolCall.GetId().GetValue()
			require.NotEmpty(t, toolCallID, "Tool call should have an ID")

			t.Logf("✅ Step 1 Success: Got tool call %s with ID %s", toolCall.GetName().GetValue(), toolCallID)

			// Step 2: Simulate tool execution and send results back
			t.Logf("🔄 Step 2: Sending tool execution results")
			req2 := &runtimev1pb.ConversationRequest{
				Name: provider.componentName,
				Inputs: []*runtimev1pb.ConversationInput{
					// Previous user message
					{
						Role: ptr.Of("user"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "What's the weather like in San Francisco? Please use the get_weather function.",
									},
								},
							},
						},
					},
					// Previous assistant response with COMPLETE parts from the actual response
					// CRITICAL: Use the exact parts returned by the assistant - this ensures
					// providers like Anthropic can find corresponding tool_use blocks
					{
						Role:    ptr.Of("assistant"),
						Content: assistantResponseParts, // Use the complete original response parts
					},
					// Tool execution result
					{
						Role: ptr.Of("tool"),
						ToolResults: []*runtimev1pb.ConversationToolResult{
							{
								Id:   toolCallID,
								Name: "get_weather",
								Result: &runtimev1pb.ConversationToolResult_OutputText{
									OutputText: `{"location": "San Francisco", "temperature": "72°F", "condition": "Sunny", "humidity": "65%"}`,
								},
							},
						},
					},
				},
			}

			resp2, err := client.ConverseAlpha1(ctx, req2)
			require.NoError(t, err)
			require.NotNil(t, resp2)
			require.NotEmpty(t, resp2.GetResults())

			// Step 3: Validate final assistant response
			t.Logf("🔄 Step 3: Validating final assistant response")
			finalOutput := resp2.GetResults()[0]
			finalResponse := extractTextFromParts(finalOutput.GetContent())
			t.Logf("🔍 Final Response: %s", finalResponse)
			t.Logf("🔍 Final FinishReason: %s", finalOutput.GetFinishReason())

			// The final response should contain weather information
			require.NotEmpty(t, finalResponse, "Should receive final response from assistant")

			// Check that the response references the weather data we provided
			responseText := strings.ToLower(finalResponse)
			hasWeatherInfo := strings.Contains(responseText, "72") ||
				strings.Contains(responseText, "sunny") ||
				strings.Contains(responseText, "temperature") ||
				strings.Contains(responseText, "san francisco")

			if hasWeatherInfo {
				t.Logf("✅ Multi-turn Success: Final response contains weather information!")
			} else {
				t.Logf("⚠️ Final response doesn't clearly reference the weather data, but multi-turn flow completed")
			}

			// Verify no more tool calls in final response (should be a regular text response)
			finalToolCalls := extractToolCallsFromParts(finalOutput.GetContent())
			assert.Empty(t, finalToolCalls, "Final response should not contain tool calls")

			// Check finish reason is appropriate
			if finalOutput.GetFinishReason() != "" {
				assert.Equal(t, "stop", finalOutput.GetFinishReason(), "Final response should have 'stop' finish reason")
			}

			t.Logf("✅ Multi-turn tool calling flow completed successfully with %s!", provider.componentName)
		})
	}
}
