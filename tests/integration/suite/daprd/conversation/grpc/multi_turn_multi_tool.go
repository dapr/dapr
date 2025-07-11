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
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/ptr"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multiTurnMultiTool))
}

type multiTurnMultiTool struct {
	daprd *daprd.Daprd
}

func (mt *multiTurnMultiTool) Setup(t *testing.T) []framework.Option {
	// Build component configuration - always include echo, conditionally include real LLMs if API keys available
	componentConfig := buildLiveAIProviderComponents(t)

	mt.daprd = daprd.New(t, daprd.WithResourceFiles(componentConfig))

	return []framework.Option{
		framework.WithProcesses(mt.daprd),
	}
}

func (mt *multiTurnMultiTool) Run(t *testing.T, ctx context.Context) {
	mt.daprd.WaitUntilRunning(t, ctx)

	client := mt.daprd.GRPCClient(t, ctx)

	// Test providers that support multi-turn multi-tool workflows
	for _, provider := range realConversationAIProviders {
		t.Run("multi_turn_multi_tool_with_"+provider.componentName, func(t *testing.T) {
			// Skip real LLM tests if API key not available
			apiKey := os.Getenv(provider.envVar)
			if apiKey == "" {
				t.Skipf("%s not set, skipping %s multi-turn multi-tool test", provider.envVar, provider.componentName)
			}

			t.Logf("🧪 Testing multi-turn multi-tool workflow with %s", provider.componentName)

			// Define multiple tools that could be called in parallel
			weatherTool := &runtimev1pb.ConversationTool{
				Type:        wrapperspb.String("function"),
				Name:        wrapperspb.String("get_weather"),
				Description: wrapperspb.String("Get current weather information for a specific location"),
				Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string","description":"The city and state/country for weather lookup"}},"required":["location"]}`),
			}

			timeTool := &runtimev1pb.ConversationTool{
				Type:        wrapperspb.String("function"),
				Name:        wrapperspb.String("get_time"),
				Description: wrapperspb.String("Get current time for a specific location or timezone"),
				Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string","description":"The city and state/country for time lookup"}},"required":["location"]}`),
			}

			exchangeRateTool := &runtimev1pb.ConversationTool{
				Type:        wrapperspb.String("function"),
				Name:        wrapperspb.String("get_exchange_rate"),
				Description: wrapperspb.String("Get current exchange rate between two currencies"),
				Parameters:  wrapperspb.String(`{"type":"object","properties":{"from_currency":{"type":"string","description":"Source currency code (e.g., USD)"},"to_currency":{"type":"string","description":"Target currency code (e.g., EUR)"}},"required":["from_currency","to_currency"]}`),
			}

			// ========== TURN 1: Request that should trigger multiple tools ==========
			t.Log("🔄 TURN 1: User asks for multiple pieces of information that require different tools")

			turn1Req := &runtimev1pb.ConversationRequest{
				Name:  provider.componentName,
				Tools: []*runtimev1pb.ConversationTool{weatherTool, timeTool, exchangeRateTool},
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Role: ptr.Of("system"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "You are a helpful assistant with access to tools. " +
											"When the user requests information that requires using available tools, call all appropriate tools (for maximum efficiency, whenever you need to perform multiple independent operations, invoke all relevant tools simultaneously rather than sequentially) " +
											"Do not provide status updates like 'I'll check that for you' or 'Let me get that information'. " +
											"Only provide text responses when: 1) You need clarification from the user, 2) The request is ambiguous and needs more details, or 3) You're presenting the final results after tool execution. Prioritize action over commentary.",
									},
								},
							},
						},
					},
					{
						Role: ptr.Of("user"),
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{
										Text: "I'm planning a trip to Tokyo. Can you get me the current weather in Tokyo, the current time there, and the USD to JPY exchange rate?",
									},
								},
							},
						},
					},
				},
			}

			turn1Resp, err := client.ConverseAlpha1(ctx, turn1Req)
			require.NoError(t, err)
			require.NotEmpty(t, turn1Resp.GetResults())

			t.Logf("📤 Turn 1 Response: %d outputs", len(turn1Resp.GetResults()))

			// Collect tool calls from ALL outputs
			var allTurn1ToolCalls []*runtimev1pb.ConversationToolCall
			var allTurn1Text strings.Builder

			for i, output := range turn1Resp.GetResults() {
				t.Logf("📋 Output %d: %d parts", i+1, len(output.GetContent()))

				// Debug: Log each part type and content
				for j, part := range output.GetContent() {
					switch content := part.GetContentType().(type) {
					case *runtimev1pb.ConversationContent_Text:
						t.Logf("🔍 Output %d, Part %d: Text=%s", i+1, j+1, content.Text.GetText())
					case *runtimev1pb.ConversationContent_ToolCall:
						t.Logf("🔍 Output %d, Part %d: ToolCall=%s(%s)", i+1, j+1, content.ToolCall.GetName(), content.ToolCall.GetArguments())
					default:
						t.Logf("🔍 Output %d, Part %d: Type=%T", i+1, j+1, content)
					}
				}

				outputText := extractTextFromParts(output.GetContent())
				outputToolCalls := extractToolCallsFromParts(output.GetContent())

				if outputText != "" {
					allTurn1Text.WriteString(outputText)
					if i < len(turn1Resp.GetResults())-1 {
						allTurn1Text.WriteString(" ")
					}
				}

				allTurn1ToolCalls = append(allTurn1ToolCalls, outputToolCalls...)

				t.Logf("📝 Output %d Text: %q", i+1, outputText)
				t.Logf("🔧 Output %d Tool Calls: %d", i+1, len(outputToolCalls))
			}

			turn1Text := allTurn1Text.String()
			turn1ToolCalls := allTurn1ToolCalls

			t.Logf("📤 Combined Turn 1 Response: %q", turn1Text)
			t.Logf("🔧 Total Turn 1 Tool Calls: %d", len(turn1ToolCalls))

			for i, toolCall := range turn1ToolCalls {
				t.Logf("🛠️  Tool Call %d: %s(%s)", i+1, toolCall.GetName(), toolCall.GetArguments())
			}

			// Track conversation history
			conversationHistory := []*runtimev1pb.ConversationInput{
				// User's question
				{
					Role: ptr.Of("user"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{
									Text: "I'm planning a trip to Tokyo. Can you get me the current weather in Tokyo, the current time there, and the USD to JPY exchange rate?",
								},
							},
						},
					},
				},
			}

			// Add each output separately to conversation history
			for i, output := range turn1Resp.GetResults() {
				t.Logf("🔄 Adding assistant output %d with %d parts to conversation history", i+1, len(output.GetContent()))
				conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
					Role:    ptr.Of("assistant"),
					Content: output.GetContent(), // Each output separately
				})
			}

			// If tools were called, process them
			if len(turn1ToolCalls) > 0 {
				t.Logf("✅ %s called %d tool(s) in Turn 1!", provider.componentName, len(turn1ToolCalls))

				// Add tool results for each tool call
				for _, toolCall := range turn1ToolCalls {
					var toolResult string
					switch toolCall.GetName().GetValue() {
					case "get_weather":
						toolResult = `{"temperature": 22, "condition": "sunny", "humidity": 60, "location": "Tokyo, Japan"}`
					case "get_time":
						toolResult = `{"time": "14:30", "timezone": "JST", "date": "2025-01-13", "location": "Tokyo, Japan"}`
					case "get_exchange_rate":
						toolResult = `{"rate": 157.25, "from": "USD", "to": "JPY", "timestamp": "2025-01-13T14:30:00Z"}`
					default:
						toolResult = `{"status": "success", "data": "mock result"}`
					}

					conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
						Role: ptr.Of("tool"),
						ToolResults: []*runtimev1pb.ConversationToolResult{
							{
								Id:   toolCall.GetId().GetValue(),
								Name: toolCall.GetName().GetValue(),
								Result: &runtimev1pb.ConversationToolResult_OutputText{
									OutputText: toolResult,
								},
							},
						},
					})
				}

				// ========== TURN 2: Process tool results ==========
				t.Log("🔄 TURN 2: Process tool results")

				turn2Req := &runtimev1pb.ConversationRequest{
					Name:   provider.componentName,
					Tools:  []*runtimev1pb.ConversationTool{weatherTool, timeTool, exchangeRateTool},
					Inputs: conversationHistory,
				}
				turn2Resp, err2 := client.ConverseAlpha1(ctx, turn2Req)
				require.NoError(t, err2)

				var allTurn2ToolCalls []*runtimev1pb.ConversationToolCall
				var allTurn2Text strings.Builder

				for i, output := range turn2Resp.GetResults() {
					outputText := extractTextFromParts(output.GetContent())
					outputToolCalls := extractToolCallsFromParts(output.GetContent())

					if outputText != "" {
						allTurn2Text.WriteString(outputText)
						if i < len(turn2Resp.GetResults())-1 {
							allTurn2Text.WriteString(" ")
						}
					}

					allTurn2ToolCalls = append(allTurn2ToolCalls, outputToolCalls...)

					t.Logf("📝 Turn 2 Output %d Text: %q", i+1, outputText)
					t.Logf("🔧 Turn 2 Output %d Tool Calls: %d", i+1, len(outputToolCalls))

					// Add each assistant output separately to conversation history
					conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
						Role:    ptr.Of("assistant"),
						Content: output.GetContent(),
					})
				}

				turn2Text := allTurn2Text.String()
				turn2ToolCalls := allTurn2ToolCalls

				t.Logf("📤 Combined Turn 2 Response: %q", turn2Text)
				t.Logf("🔧 Total Turn 2 Tool Calls: %d", len(turn2ToolCalls))

				// Check if provider made additional tool calls (sequential behavior)
				if len(turn2ToolCalls) > 0 {
					t.Log("🔄 Provider made additional tool calls - continuing sequential pattern")

					// Process additional tool calls (same pattern as conformance tests)
					for _, toolCall := range turn2ToolCalls {
						var toolResult string
						switch toolCall.GetName().GetValue() {
						case "get_weather":
							toolResult = `{"temperature": 22, "condition": "sunny", "humidity": 60, "location": "Tokyo, Japan"}`
						case "get_time":
							toolResult = `{"time": "14:30", "timezone": "JST", "date": "2025-01-13", "location": "Tokyo, Japan"}`
						case "get_exchange_rate":
							toolResult = `{"rate": 157.25, "from": "USD", "to": "JPY", "timestamp": "2025-01-13T14:30:00Z"}`
						default:
							toolResult = `{"status": "success", "data": "mock result"}`
						}

						conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
							Role: ptr.Of("tool"),
							ToolResults: []*runtimev1pb.ConversationToolResult{
								{
									Id:   toolCall.GetId().GetValue(),
									Name: toolCall.GetName().GetValue(),
									Result: &runtimev1pb.ConversationToolResult_OutputText{
										OutputText: toolResult,
									},
								},
							},
						})
					}

					// ========== TURN 3: Process additional tool results ==========
					t.Log("🔄 TURN 3: Process additional tool results")

					turn3Req := &runtimev1pb.ConversationRequest{
						Name:   provider.componentName,
						Inputs: conversationHistory,
					}
					turn3Resp, err3 := client.ConverseAlpha1(ctx, turn3Req)
					require.NoError(t, err3)

					turn3Text := extractTextFromParts(turn3Resp.GetResults()[0].GetContent())
					t.Logf("📤 Turn 3 Response: %q", turn3Text)

					// Validate comprehensive response
					assert.NotEmpty(t, turn3Text, "Should provide comprehensive response using all tool results")

					t.Logf("✅ %s sequential multi-tool test completed with %d + %d tool calls", provider.componentName, len(turn1ToolCalls), len(turn2ToolCalls))
				} else {
					// No additional tool calls - validate tool result processing
					if len(turn1ToolCalls) >= 2 {
						// Look for evidence that multiple tool results were used
						hasWeatherData := strings.Contains(turn2Text, "22") || strings.Contains(turn2Text, "sunny") || strings.Contains(turn2Text, "temperature")
						hasTimeData := strings.Contains(turn2Text, "14:30") || strings.Contains(turn2Text, "JST") || strings.Contains(turn2Text, "time")
						hasExchangeData := strings.Contains(turn2Text, "157") || strings.Contains(turn2Text, "JPY") || strings.Contains(turn2Text, "rate")

						toolsUsedCount := 0
						if hasWeatherData {
							toolsUsedCount++
						}
						if hasTimeData {
							toolsUsedCount++
						}
						if hasExchangeData {
							toolsUsedCount++
						}

						t.Logf("🎯 %s integrated %d/%d tool results in response", provider.componentName, toolsUsedCount, len(turn1ToolCalls))

						if toolsUsedCount >= 2 {
							t.Log("🎯 SUCCESS: Provider response incorporates data from multiple tool calls!")
						}
					}

					assert.NotEmpty(t, turn2Text, "Should provide response using tool results")
					t.Logf("✅ %s parallel multi-tool test completed with %d tool calls", provider.componentName, len(turn1ToolCalls))
				}

				// ========== TURN 4: Follow-up question to test conversation memory ==========
				t.Log("🔄 TURN 4: Follow-up question to test conversation memory")

				conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
					Role: ptr.Of("user"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{
									Text: "Based on that weather and time information, what would be the best time to visit Tokyo today?",
								},
							},
						},
					},
				})

				turn4Req := &runtimev1pb.ConversationRequest{
					Name:   provider.componentName,
					Inputs: conversationHistory,
				}
				turn4Resp, err4 := client.ConverseAlpha1(ctx, turn4Req)
				require.NoError(t, err4, "Failed to converse with %s: %v", provider.componentName, err4)

				turn4Text := extractTextFromParts(turn4Resp.GetResults()[0].GetContent())
				t.Logf("📤 Turn 4 Response: %q", turn4Text)

				// Validate conversation memory and logical recommendations
				assert.NotEmpty(t, turn4Text, "Should provide recommendations based on previous information")
				assert.Contains(t, turn4Text, "Tokyo", "Should reference Tokyo from conversation context")

				// Check for evidence of using previous tool results
				hasContextualResponse := strings.Contains(turn4Text, "sunny") ||
					strings.Contains(turn4Text, "22") ||
					strings.Contains(turn4Text, "afternoon") ||
					strings.Contains(turn4Text, "weather") ||
					strings.Contains(turn4Text, "time")

				if hasContextualResponse {
					t.Log("🎯 SUCCESS: Provider shows conversation memory by referencing previous tool results!")
				}

				t.Logf("✅ %s multi-turn conversation memory test completed", provider.componentName)
				return
			}

			// If provider didn't call tools in Turn 1, test follow-up approach
			t.Logf("⚠️ %s didn't call tools in Turn 1 - testing follow-up approach", provider.componentName)

			// Add follow-up to encourage tool usage
			conversationHistory = append(conversationHistory, &runtimev1pb.ConversationInput{
				Role: ptr.Of("user"),
				Content: []*runtimev1pb.ConversationContent{
					{
						ContentType: &runtimev1pb.ConversationContent_Text{
							Text: &runtimev1pb.ConversationText{
								Text: "Please get that information for me using the available tools.",
							},
						},
					},
				},
			})

			turn2Req := &runtimev1pb.ConversationRequest{
				Name:   provider.componentName,
				Inputs: conversationHistory,
			}
			turn2Resp, err5 := client.ConverseAlpha1(ctx, turn2Req)
			require.NoError(t, err5)

			turn2ToolCalls := extractToolCallsFromParts(turn2Resp.GetResults()[0].GetContent())
			turn2Text := extractTextFromParts(turn2Resp.GetResults()[0].GetContent())

			t.Logf("📤 Turn 2 Response: %q", turn2Text)
			t.Logf("🔧 Turn 2 Tool Calls: %d", len(turn2ToolCalls))

			if len(turn2ToolCalls) > 0 {
				t.Logf("🎯 %s called %d tool(s) in Turn 2 after follow-up!", provider.componentName, len(turn2ToolCalls))
				t.Logf("✅ %s responsive tool calling validated!", provider.componentName)
			} else {
				t.Logf("⚠️ %s still not calling tools - conservative behavior", provider.componentName)
				// For echo component, just validate it responded (echo doesn't have conversation memory)
				if provider.componentName == "echo" {
					assert.NotEmpty(t, turn2Text, "Echo should respond with some text")
				} else {
					// For real LLMs, validate conversation memory
					assert.Contains(t, turn2Text, "Tokyo", "Should remember the location from previous turn")
				}
			}

			t.Logf("✅ %s multi-turn multi-tool test completed", provider.componentName)
		})
	}
}

// Helper function to extract text content from parts
func extractTextFromParts(parts []*runtimev1pb.ConversationContent) string {
	var textParts []string
	for _, part := range parts {
		if textContent := part.GetText(); textContent != nil {
			textParts = append(textParts, textContent.GetText())
		}
	}
	return strings.Join(textParts, " ")
}
