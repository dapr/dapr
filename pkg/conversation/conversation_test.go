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
	"testing"

	piiscrubber "github.com/aavaz-ai/pii-scrubber"
	contribConverse "github.com/dapr/components-contrib/conversation"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestConvertUsageToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    *contribConverse.ConversationResponse
		expected *runtimev1pb.ConversationUsage
	}{
		{
			name:     "nil response",
			input:    nil,
			expected: nil,
		},
		{
			name: "response with nil usage",
			input: &contribConverse.ConversationResponse{
				Usage: nil,
			},
			expected: nil,
		},
		{
			name: "response with empty usage",
			input: &contribConverse.ConversationResponse{
				Usage: &contribConverse.UsageInfo{},
			},
			expected: &runtimev1pb.ConversationUsage{
				PromptTokens:     ptr.Of(uint32(0)),
				CompletionTokens: ptr.Of(uint32(0)),
				TotalTokens:      ptr.Of(uint32(0)),
			},
		},
		{
			name: "response with full usage",
			input: &contribConverse.ConversationResponse{
				Usage: &contribConverse.UsageInfo{
					PromptTokens:     100,
					CompletionTokens: 50,
					TotalTokens:      150,
				},
			},
			expected: &runtimev1pb.ConversationUsage{
				PromptTokens:     ptr.Of(uint32(100)),
				CompletionTokens: ptr.Of(uint32(50)),
				TotalTokens:      ptr.Of(uint32(150)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertUsageToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNeedsInputScrubber(t *testing.T) {
	tests := []struct {
		name     string
		input    *runtimev1pb.ConversationRequest
		expected bool
	}{
		{
			name: "no inputs",
			input: &runtimev1pb.ConversationRequest{
				Inputs: nil,
			},
			expected: false,
		},
		{
			name: "inputs without PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Inputs: []*runtimev1pb.ConversationInput{
					{Content: "hello", ScrubPII: ptr.Of(false)},
					{Content: "world", ScrubPII: nil},
				},
			},
			expected: false,
		},
		{
			name: "one input with PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Inputs: []*runtimev1pb.ConversationInput{
					{Content: "hello", ScrubPII: ptr.Of(false)},
					{Content: "my email is test@example.com", ScrubPII: ptr.Of(true)},
				},
			},
			expected: true,
		},
		{
			name: "all inputs with PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Inputs: []*runtimev1pb.ConversationInput{
					{Content: "hello", ScrubPII: ptr.Of(true)},
					{Content: "world", ScrubPII: ptr.Of(true)},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NeedsInputScrubber(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNeedsOutputScrubber(t *testing.T) {
	tests := []struct {
		name     string
		input    *runtimev1pb.ConversationRequest
		expected bool
	}{
		{
			name: "no output scrubbing",
			input: &runtimev1pb.ConversationRequest{
				ScrubPII: ptr.Of(false),
			},
			expected: false,
		},
		{
			name: "nil output scrubbing",
			input: &runtimev1pb.ConversationRequest{
				ScrubPII: nil,
			},
			expected: false,
		},
		{
			name: "output scrubbing enabled",
			input: &runtimev1pb.ConversationRequest{
				ScrubPII: ptr.Of(true),
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NeedsOutputScrubber(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertProtoToolsToComponentsContrib(t *testing.T) {
	tests := []struct {
		name     string
		input    []*runtimev1pb.Tool
		expected []contribConverse.Tool
	}{
		{
			name:     "nil tools",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty tools",
			input:    []*runtimev1pb.Tool{},
			expected: nil,
		},
		{
			name: "single tool",
			input: []*runtimev1pb.Tool{
				{
					Type:        "function",
					Name:        "get_weather",
					Description: "Get weather information",
					Parameters:  `{"type": "object"}`,
				},
			},
			expected: []contribConverse.Tool{
				{
					ToolType: "function",
					Function: contribConverse.ToolFunction{
						Name:        "get_weather",
						Description: "Get weather information",
						Parameters:  `{"type": "object"}`,
					},
				},
			},
		},
		{
			name: "multiple tools",
			input: []*runtimev1pb.Tool{
				{
					Type:        "function",
					Name:        "get_weather",
					Description: "Get weather information",
					Parameters:  `{"type": "object", "properties": {"city": {"type": "string"}}}`,
				},
				{
					Type:        "function",
					Name:        "send_email",
					Description: "Send an email",
					Parameters:  `{"type": "object", "properties": {"to": {"type": "string"}, "subject": {"type": "string"}}}`,
				},
			},
			expected: []contribConverse.Tool{
				{
					ToolType: "function",
					Function: contribConverse.ToolFunction{
						Name:        "get_weather",
						Description: "Get weather information",
						Parameters:  `{"type": "object", "properties": {"city": {"type": "string"}}}`,
					},
				},
				{
					ToolType: "function",
					Function: contribConverse.ToolFunction{
						Name:        "send_email",
						Description: "Send an email",
						Parameters:  `{"type": "object", "properties": {"to": {"type": "string"}, "subject": {"type": "string"}}}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertProtoToolsToComponentsContrib(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertComponentsContribToolCallsToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    []contribConverse.ToolCall
		expected []*runtimev1pb.ToolCall
	}{
		{
			name:     "nil tool calls",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty tool calls",
			input:    []contribConverse.ToolCall{},
			expected: nil,
		},
		{
			name: "single tool call",
			input: []contribConverse.ToolCall{
				{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
			},
			expected: []*runtimev1pb.ToolCall{
				{
					Id:        "call_123",
					Type:      "function",
					Name:      "get_weather",
					Arguments: `{"city": "San Francisco"}`,
				},
			},
		},
		{
			name: "multiple tool calls",
			input: []contribConverse.ToolCall{
				{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
				{
					ID:       "call_456",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "send_email",
						Arguments: `{"to": "user@example.com", "subject": "Test"}`,
					},
				},
			},
			expected: []*runtimev1pb.ToolCall{
				{
					Id:        "call_123",
					Type:      "function",
					Name:      "get_weather",
					Arguments: `{"city": "San Francisco"}`,
				},
				{
					Id:        "call_456",
					Type:      "function",
					Name:      "send_email",
					Arguments: `{"to": "user@example.com", "subject": "Test"}`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertComponentsContribToolCallsToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractToolCallIDFromParts(t *testing.T) {
	tests := []struct {
		name     string
		input    []*runtimev1pb.ContentPart
		expected string
	}{
		{
			name:     "empty parts",
			input:    []*runtimev1pb.ContentPart{},
			expected: "",
		},
		{
			name: "text part only",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "hello"},
					},
				},
			},
			expected: "",
		},
		{
			name: "tool result part",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 72°F",
						},
					},
				},
			},
			expected: "call_123",
		},
		{
			name: "multiple parts with tool result",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "hello"},
					},
				},
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_456",
							Name:       "send_email",
							Content:    "Email sent successfully",
						},
					},
				},
			},
			expected: "call_456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractToolCallIDFromParts(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractToolNameFromParts(t *testing.T) {
	tests := []struct {
		name     string
		input    []*runtimev1pb.ContentPart
		expected string
	}{
		{
			name:     "empty parts",
			input:    []*runtimev1pb.ContentPart{},
			expected: "",
		},
		{
			name: "text part only",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "hello"},
					},
				},
			},
			expected: "",
		},
		{
			name: "tool result part",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 72°F",
						},
					},
				},
			},
			expected: "get_weather",
		},
		{
			name: "multiple parts with tool result",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "hello"},
					},
				},
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_456",
							Name:       "send_email",
							Content:    "Email sent successfully",
						},
					},
				},
			},
			expected: "send_email",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractToolNameFromParts(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertComponentsContribPartsToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []*runtimev1pb.ContentPart
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "non-empty string",
			input: "Hello, world!",
			expected: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{
							Text: "Hello, world!",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertComponentsContribPartsToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertProtoPartsToComponentsContrib(t *testing.T) {
	tests := []struct {
		name     string
		input    []*runtimev1pb.ContentPart
		expected []contribConverse.ContentPart
	}{
		{
			name:     "nil parts",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty parts",
			input:    []*runtimev1pb.ContentPart{},
			expected: nil,
		},
		{
			name: "text part",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "Hello, world!"},
					},
				},
			},
			expected: []contribConverse.ContentPart{
				contribConverse.TextContentPart{Text: "Hello, world!"},
			},
		},
		{
			name: "tool call part",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolCall{
						ToolCall: &runtimev1pb.ToolCallContent{
							Id:        "call_123",
							Type:      "function",
							Name:      "get_weather",
							Arguments: `{"city": "San Francisco"}`,
						},
					},
				},
			},
			expected: []contribConverse.ContentPart{
				contribConverse.ToolCallContentPart{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
			},
		},
		{
			name: "tool result part",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 72°F",
							IsError:    ptr.Of(false),
						},
					},
				},
			},
			expected: []contribConverse.ContentPart{
				contribConverse.ToolResultContentPart{
					ToolCallID: "call_123",
					Name:       "get_weather",
					Content:    "Sunny, 72°F",
					IsError:    false,
				},
			},
		},
		{
			name: "mixed parts",
			input: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "Let me check the weather"},
					},
				},
				{
					ContentType: &runtimev1pb.ContentPart_ToolCall{
						ToolCall: &runtimev1pb.ToolCallContent{
							Id:        "call_123",
							Type:      "function",
							Name:      "get_weather",
							Arguments: `{"city": "San Francisco"}`,
						},
					},
				},
			},
			expected: []contribConverse.ContentPart{
				contribConverse.TextContentPart{Text: "Let me check the weather"},
				contribConverse.ToolCallContentPart{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertProtoPartsToComponentsContrib(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertComponentsContribContentPartsToProto(t *testing.T) {
	// Create a mock PII scrubber for testing
	scrubber, err := piiscrubber.NewDefaultScrubber()
	require.NoError(t, err)

	tests := []struct {
		name          string
		input         []contribConverse.ContentPart
		scrubber      piiscrubber.Scrubber
		componentName string
		expected      []*runtimev1pb.ContentPart
		expectError   bool
	}{
		{
			name:     "nil parts",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty parts",
			input:    []contribConverse.ContentPart{},
			expected: nil,
		},
		{
			name: "text part without scrubber",
			input: []contribConverse.ContentPart{
				contribConverse.TextContentPart{Text: "Hello, world!"},
			},
			expected: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "Hello, world!"},
					},
				},
			},
		},
		{
			name: "text part with scrubber",
			input: []contribConverse.ContentPart{
				contribConverse.TextContentPart{Text: "My email is test@example.com"},
			},
			scrubber:      scrubber,
			componentName: "test-component",
			expected: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_Text{
						Text: &runtimev1pb.TextContent{Text: "My email is <EMAIL_ADDRESS>"},
					},
				},
			},
		},
		{
			name: "tool call part",
			input: []contribConverse.ContentPart{
				contribConverse.ToolCallContentPart{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
			},
			expected: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolCall{
						ToolCall: &runtimev1pb.ToolCallContent{
							Id:        "call_123",
							Type:      "function",
							Name:      "get_weather",
							Arguments: `{"city": "San Francisco"}`,
						},
					},
				},
			},
		},
		{
			name: "tool result part",
			input: []contribConverse.ContentPart{
				contribConverse.ToolResultContentPart{
					ToolCallID: "call_123",
					Name:       "get_weather",
					Content:    "Sunny, 72°F",
					IsError:    false,
				},
			},
			expected: []*runtimev1pb.ContentPart{
				{
					ContentType: &runtimev1pb.ContentPart_ToolResult{
						ToolResult: &runtimev1pb.ToolResultContent{
							ToolCallId: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 72°F",
							IsError:    ptr.Of(false),
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convertComponentsContribContentPartsToProto(tt.input, tt.scrubber, tt.componentName)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetInputsFromRequest(t *testing.T) {
	// Create a mock PII scrubber for testing
	scrubber, err := piiscrubber.NewDefaultScrubber()
	require.NoError(t, err)

	tests := []struct {
		name        string
		input       *runtimev1pb.ConversationRequest
		scrubber    piiscrubber.Scrubber
		expected    []contribConverse.ConversationInput
		expectError bool
	}{
		{
			name: "nil inputs",
			input: &runtimev1pb.ConversationRequest{
				Name:   "test-component",
				Inputs: nil,
			},
			expectError: true,
		},
		{
			name: "legacy content without PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Name: "test-component",
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Content:  "Hello, world!",
						Role:     ptr.Of("user"),
						ScrubPII: ptr.Of(false),
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Message: "Hello, world!",
					Role:    contribConverse.RoleUser,
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "Hello, world!"},
					},
				},
			},
		},
		{
			name: "legacy content with PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Name: "test-component",
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Content:  "My email is test@example.com",
						Role:     ptr.Of("user"),
						ScrubPII: ptr.Of(true),
					},
				},
			},
			scrubber: scrubber,
			expected: []contribConverse.ConversationInput{
				{
					Message: "My email is <EMAIL_ADDRESS>",
					Role:    contribConverse.RoleUser,
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "My email is <EMAIL_ADDRESS>"},
					},
				},
			},
		},
		{
			name: "parts-based content",
			input: &runtimev1pb.ConversationRequest{
				Name: "test-component",
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Role: ptr.Of("user"),
						Parts: []*runtimev1pb.ContentPart{
							{
								ContentType: &runtimev1pb.ContentPart_Text{
									Text: &runtimev1pb.TextContent{Text: "Hello, world!"},
								},
							},
						},
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Message: "Hello, world!",
					Role:    contribConverse.RoleUser,
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "Hello, world!"},
					},
				},
			},
		},
		{
			name: "tool result with role detection",
			input: &runtimev1pb.ConversationRequest{
				Name: "test-component",
				Inputs: []*runtimev1pb.ConversationInput{
					{
						Role: ptr.Of("user"), // Will be changed to tool due to tool result content
						Parts: []*runtimev1pb.ContentPart{
							{
								ContentType: &runtimev1pb.ContentPart_ToolResult{
									ToolResult: &runtimev1pb.ToolResultContent{
										ToolCallId: "call_123",
										Name:       "get_weather",
										Content:    "Sunny, 72°F",
									},
								},
							},
						},
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Message: "",
					Role:    contribConverse.RoleTool,
					Parts: []contribConverse.ContentPart{
						contribConverse.ToolResultContentPart{
							ToolCallID: "call_123",
							Name:       "get_weather",
							Content:    "Sunny, 72°F",
							IsError:    false,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetInputsFromRequest(tt.input, tt.scrubber)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, messages.ErrConversationMissingInputs)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestConvertComponentsContribOutputToProto(t *testing.T) {
	// Create a mock PII scrubber for testing
	scrubber, err := piiscrubber.NewDefaultScrubber()
	require.NoError(t, err)

	// Create some test parameters
	testParams, err := createTestParameters()
	require.NoError(t, err)

	tests := []struct {
		name          string
		input         []contribConverse.ConversationOutput
		scrubber      piiscrubber.Scrubber
		componentName string
		expected      []*runtimev1pb.ConversationResult
		expectError   bool
	}{
		{
			name:     "nil outputs",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty outputs",
			input:    []contribConverse.ConversationOutput{},
			expected: nil,
		},
		{
			name: "legacy result without PII scrubbing",
			input: []contribConverse.ConversationOutput{
				{
					Result:     "Hello, world!",
					Parameters: testParams,
				},
			},
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Result:       "Hello, world!",
					Parameters:   testParams,
					FinishReason: ptr.Of("stop"),
					Parts: []*runtimev1pb.ContentPart{
						{
							ContentType: &runtimev1pb.ContentPart_Text{
								Text: &runtimev1pb.TextContent{Text: "Hello, world!"},
							},
						},
					},
				},
			},
		},
		{
			name: "legacy result with PII scrubbing",
			input: []contribConverse.ConversationOutput{
				{
					Result:     "My email is test@example.com",
					Parameters: testParams,
				},
			},
			scrubber:      scrubber,
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Result:       "My email is <EMAIL_ADDRESS>",
					Parameters:   testParams,
					FinishReason: ptr.Of("stop"),
					Parts: []*runtimev1pb.ContentPart{
						{
							ContentType: &runtimev1pb.ContentPart_Text{
								Text: &runtimev1pb.TextContent{Text: "My email is <EMAIL_ADDRESS>"},
							},
						},
					},
				},
			},
		},
		{
			name: "parts-based output with tool calls",
			input: []contribConverse.ConversationOutput{
				{
					Parameters: testParams,
					Parts: []contribConverse.ContentPart{
						contribConverse.TextContentPart{Text: "Let me check the weather"},
						contribConverse.ToolCallContentPart{
							ID:       "call_123",
							CallType: "function",
							Function: contribConverse.ToolCallFunction{
								Name:      "get_weather",
								Arguments: `{"city": "San Francisco"}`,
							},
						},
					},
				},
			},
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Result:       "",
					Parameters:   testParams,
					FinishReason: ptr.Of("tool_calls"),
					Parts: []*runtimev1pb.ContentPart{
						{
							ContentType: &runtimev1pb.ContentPart_Text{
								Text: &runtimev1pb.TextContent{Text: "Let me check the weather"},
							},
						},
						{
							ContentType: &runtimev1pb.ContentPart_ToolCall{
								ToolCall: &runtimev1pb.ToolCallContent{
									Id:        "call_123",
									Type:      "function",
									Name:      "get_weather",
									Arguments: `{"city": "San Francisco"}`,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "custom finish reason",
			input: []contribConverse.ConversationOutput{
				{
					Result:       "Response truncated",
					FinishReason: "length",
					Parameters:   testParams,
				},
			},
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Result:       "Response truncated",
					Parameters:   testParams,
					FinishReason: ptr.Of("length"),
					Parts: []*runtimev1pb.ContentPart{
						{
							ContentType: &runtimev1pb.ContentPart_Text{
								Text: &runtimev1pb.TextContent{Text: "Response truncated"},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ConvertComponentsContribOutputToProto(tt.input, tt.scrubber, tt.componentName)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// Helper function to create test parameters
func createTestParameters() (map[string]*anypb.Any, error) {
	// Create a simple parameter map
	tempParam, err := anypb.New(&runtimev1pb.ConversationRequest{Temperature: ptr.Of(0.7)})
	if err != nil {
		return nil, err
	}

	return map[string]*anypb.Any{
		"temperature": tempParam,
	}, nil
}
