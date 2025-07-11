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
	"google.golang.org/protobuf/types/known/wrapperspb"
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
				PromptTokens:     ptr.Of(uint64(0)),
				CompletionTokens: ptr.Of(uint64(0)),
				TotalTokens:      ptr.Of(uint64(0)),
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
				PromptTokens:     ptr.Of(uint64(100)),
				CompletionTokens: ptr.Of(uint64(50)),
				TotalTokens:      ptr.Of(uint64(150)),
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
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "hello"}}},
					}, ScrubPII: ptr.Of(false)},
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "world"}}},
					}, ScrubPII: nil},
				},
			},
			expected: false,
		},
		{
			name: "one input with PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Inputs: []*runtimev1pb.ConversationInput{
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "hello"}}},
					}, ScrubPII: ptr.Of(false)},
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "my email is test@example.com"}}},
					}, ScrubPII: ptr.Of(true)},
				},
			},
			expected: true,
		},
		{
			name: "all inputs with PII scrubbing",
			input: &runtimev1pb.ConversationRequest{
				Inputs: []*runtimev1pb.ConversationInput{
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "hello"}}},
					}, ScrubPII: ptr.Of(true)},
					{Content: []*runtimev1pb.ConversationContent{
						{ContentType: &runtimev1pb.ConversationContent_Text{Text: &runtimev1pb.ConversationText{Text: "world"}}},
					}, ScrubPII: ptr.Of(true)},
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
		input    []*runtimev1pb.ConversationTool
		expected []contribConverse.Tool
	}{
		{
			name:     "nil tools",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty tools",
			input:    []*runtimev1pb.ConversationTool{},
			expected: nil,
		},
		{
			name: "single tool",
			input: []*runtimev1pb.ConversationTool{
				{
					Type:        wrapperspb.String("function"),
					Name:        wrapperspb.String("get_weather"),
					Description: wrapperspb.String("Get weather information"),
					Parameters:  wrapperspb.String(`{"type": "object"}`),
				},
			},
			expected: []contribConverse.Tool{
				{
					Type: "function",
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
			input: []*runtimev1pb.ConversationTool{
				{
					Type:        wrapperspb.String("function"),
					Name:        wrapperspb.String("get_weather"),
					Description: wrapperspb.String("Get weather information"),
					Parameters:  wrapperspb.String(`{"type": "object", "properties": {"city": {"type": "string"}}}`),
				},
				{
					Type:        wrapperspb.String("function"),
					Name:        wrapperspb.String("send_email"),
					Description: wrapperspb.String("Send an email"),
					Parameters:  wrapperspb.String(`{"type": "object", "properties": {"to": {"type": "string"}, "subject": {"type": "string"}}}`),
				},
			},
			expected: []contribConverse.Tool{
				{
					Type: "function",
					Function: contribConverse.ToolFunction{
						Name:        "get_weather",
						Description: "Get weather information",
						Parameters:  `{"type": "object", "properties": {"city": {"type": "string"}}}`,
					},
				},
				{
					Type: "function",
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
		expected []*runtimev1pb.ConversationToolCall
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
			expected: []*runtimev1pb.ConversationToolCall{
				{
					Id:        wrapperspb.String("call_123"),
					Type:      wrapperspb.String("function"),
					Name:      wrapperspb.String("get_weather"),
					Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
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
			expected: []*runtimev1pb.ConversationToolCall{
				{
					Id:        wrapperspb.String("call_123"),
					Type:      wrapperspb.String("function"),
					Name:      wrapperspb.String("get_weather"),
					Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
				},
				{
					Id:        wrapperspb.String("call_456"),
					Type:      wrapperspb.String("function"),
					Name:      wrapperspb.String("send_email"),
					Arguments: wrapperspb.String(`{"to": "user@example.com", "subject": "Test"}`),
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

func TestConvertComponentsContribPartsToProto(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []*runtimev1pb.ConversationContent
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "non-empty string",
			input: "Hello, world!",
			expected: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_Text{
						Text: &runtimev1pb.ConversationText{
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
		input    []*runtimev1pb.ConversationContent
		expected []contribConverse.ConversationContent
	}{
		{
			name:     "nil parts",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty parts",
			input:    []*runtimev1pb.ConversationContent{},
			expected: nil,
		},
		{
			name: "text part",
			input: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_Text{
						Text: &runtimev1pb.ConversationText{Text: "Hello, world!"},
					},
				},
			},
			expected: []contribConverse.ConversationContent{
				contribConverse.TextContentPart{Text: "Hello, world!"},
			},
		},
		{
			name: "tool call part",
			input: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_ToolCall{
						ToolCall: &runtimev1pb.ConversationToolCall{
							Id:        wrapperspb.String("call_123"),
							Type:      wrapperspb.String("function"),
							Name:      wrapperspb.String("get_weather"),
							Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
						},
					},
				},
			},
			expected: []contribConverse.ConversationContent{
				contribConverse.ToolCallRequest{
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
			name: "mixed parts",
			input: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_Text{
						Text: &runtimev1pb.ConversationText{Text: "Let me check the weather"},
					},
				},
				{
					ContentType: &runtimev1pb.ConversationContent_ToolCall{
						ToolCall: &runtimev1pb.ConversationToolCall{
							Id:        wrapperspb.String("call_123"),
							Type:      wrapperspb.String("function"),
							Name:      wrapperspb.String("get_weather"),
							Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
						},
					},
				},
			},
			expected: []contribConverse.ConversationContent{
				contribConverse.TextContentPart{Text: "Let me check the weather"},
				contribConverse.ToolCallRequest{
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

// TODO(@Sicoyle): I moved &runtimev1pb.ConversationToolResul from ConversationContent to ConversationInput.ToolResults.
// So somewhere else now I need to doulbe check that on multi-turn conversations that I maintain tool results as I should..
func TestConvertComponentsContribContentPartsToProto(t *testing.T) {
	// Create a mock PII scrubber for testing
	scrubber, err := piiscrubber.NewDefaultScrubber()
	require.NoError(t, err)

	tests := []struct {
		name          string
		input         []contribConverse.ConversationContent
		scrubber      piiscrubber.Scrubber
		componentName string
		expected      []*runtimev1pb.ConversationContent
		expectError   bool
	}{
		{
			name:     "nil parts",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty parts",
			input:    []contribConverse.ConversationContent{},
			expected: nil,
		},
		{
			name: "text part without scrubber",
			input: []contribConverse.ConversationContent{
				contribConverse.TextContentPart{Text: "Hello, world!"},
			},
			expected: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_Text{
						Text: &runtimev1pb.ConversationText{Text: "Hello, world!"},
					},
				},
			},
		},
		{
			name: "text part with scrubber",
			input: []contribConverse.ConversationContent{
				contribConverse.TextContentPart{Text: "My email is test@example.com"},
			},
			scrubber:      scrubber,
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_Text{
						Text: &runtimev1pb.ConversationText{Text: "My email is <EMAIL_ADDRESS>"},
					},
				},
			},
		},
		{
			name: "tool call part",
			input: []contribConverse.ConversationContent{
				contribConverse.ToolCallRequest{
					ID:       "call_123",
					CallType: "function",
					Function: contribConverse.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city": "San Francisco"}`,
					},
				},
			},
			expected: []*runtimev1pb.ConversationContent{
				{
					ContentType: &runtimev1pb.ConversationContent_ToolCall{
						ToolCall: &runtimev1pb.ConversationToolCall{
							Id:        wrapperspb.String("call_123"),
							Type:      wrapperspb.String("function"),
							Name:      wrapperspb.String("get_weather"),
							Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
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
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{Text: "Hello, world!"},
								},
							},
						},
						Role:     ptr.Of("user"),
						ScrubPII: ptr.Of(false),
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Role: contribConverse.RoleUser,
					Content: []contribConverse.ConversationContent{
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
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{Text: "My email is test@example.com"},
								},
							},
						},
						Role:     ptr.Of("user"),
						ScrubPII: ptr.Of(true),
					},
				},
			},
			scrubber: scrubber,
			expected: []contribConverse.ConversationInput{
				{
					Role: contribConverse.RoleUser,
					Content: []contribConverse.ConversationContent{
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
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_Text{
									Text: &runtimev1pb.ConversationText{Text: "Hello, world!"},
								},
							},
						},
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Role: contribConverse.RoleUser,
					Content: []contribConverse.ConversationContent{
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
						Content: []*runtimev1pb.ConversationContent{
							{
								ContentType: &runtimev1pb.ConversationContent_ToolCall{
									ToolCall: &runtimev1pb.ConversationToolCall{
										Id:   wrapperspb.String("call_123"),
										Type: wrapperspb.String("function"),
										Name: wrapperspb.String("get_weather"),
										// TODO(@Sicoyle): double check i have a test checking args field
									},
								},
							},
						},
					},
				},
			},
			expected: []contribConverse.ConversationInput{
				{
					Role: contribConverse.RoleTool,
					Content: []contribConverse.ConversationContent{
						contribConverse.ToolCallResponse{
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
					Content: []contribConverse.ConversationContent{
						contribConverse.TextContentPart{
							Text: "Hello, world!",
						},
					},
					Parameters: testParams,
				},
			},
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Parameters:   testParams,
					FinishReason: ptr.Of("stop"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{
									Text: "Hello, world!",
								},
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
					Content: []contribConverse.ConversationContent{
						contribConverse.TextContentPart{
							Text: "My email is test@example.com",
						},
					},
					Parameters: testParams,
				},
			},
			scrubber:      scrubber,
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Parameters:   testParams,
					FinishReason: ptr.Of("stop"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{Text: "My email is <EMAIL_ADDRESS>"},
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
					Content: []contribConverse.ConversationContent{
						contribConverse.TextContentPart{
							Text: "Let me check the weather",
						},
						contribConverse.ToolCallRequest{
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
					Parameters:   testParams,
					FinishReason: ptr.Of("tool_calls"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{Text: "Let me check the weather"},
							},
						},
						{
							ContentType: &runtimev1pb.ConversationContent_ToolCall{
								ToolCall: &runtimev1pb.ConversationToolCall{
									Id:        wrapperspb.String("call_123"),
									Type:      wrapperspb.String("function"),
									Name:      wrapperspb.String("get_weather"),
									Arguments: wrapperspb.String(`{"city": "San Francisco"}`),
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
					Content: []contribConverse.ConversationContent{
						contribConverse.TextContentPart{
							Text: "Response truncated",
						},
					},
					FinishReason: "length",
					Parameters:   testParams,
				},
			},
			componentName: "test-component",
			expected: []*runtimev1pb.ConversationResult{
				{
					Parameters:   testParams,
					FinishReason: ptr.Of("length"),
					Content: []*runtimev1pb.ConversationContent{
						{
							ContentType: &runtimev1pb.ConversationContent_Text{
								Text: &runtimev1pb.ConversationText{Text: "Response truncated"},
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
