package universal

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation/echo"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/ptr"
)

func TestMapRoleToChatMessageType(t *testing.T) {
	testCases := []struct {
		input    string
		expected llms.ChatMessageType
	}{
		{"user", llms.ChatMessageTypeHuman},
		{"human", llms.ChatMessageTypeHuman},
		{"system", llms.ChatMessageTypeSystem},
		{"assistant", llms.ChatMessageTypeAI},
		{"ai", llms.ChatMessageTypeAI},
		{"tool", llms.ChatMessageTypeTool},
		{"function", llms.ChatMessageTypeFunction},
		{"generic", llms.ChatMessageTypeGeneric},
		{"", llms.ChatMessageTypeHuman},
		{"unknown", llms.ChatMessageTypeHuman},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := mapRoleToChatMessageType(tc.input)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestConverseAlpha2InvalidParams asserts that every ErrConversationInvalidParams
// branch returns an actionable message that includes the concrete reason and does
// not leak Go's %!(EXTRA ...) formatting sentinel. Passing an extra argument to a
// single-verb format template appended that sentinel to the caller-facing error.
func TestConverseAlpha2InvalidParams(t *testing.T) {
	const componentName = "test-echo"

	compStore := compstore.New()
	compStore.AddConversation(componentName, echo.NewEcho(testLogger))

	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	tests := map[string]struct {
		req            *runtimev1pb.ConversationRequestAlpha2
		expectedReason string
	}{
		"nil message type": {
			req: &runtimev1pb.ConversationRequestAlpha2{
				Name: componentName,
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{{}},
				}},
			},
			expectedReason: "message type cannot be nil",
		},
		"nil tool types": {
			req: &runtimev1pb.ConversationRequestAlpha2{
				Name: componentName,
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{{
						MessageTypes: &runtimev1pb.ConversationMessage_OfAssistant{
							OfAssistant: &runtimev1pb.ConversationMessageOfAssistant{
								ToolCalls: []*runtimev1pb.ConversationToolCalls{{}},
							},
						},
					}},
				}},
			},
			expectedReason: "tool types cannot be nil",
		},
		"tool choice required without tools": {
			req: &runtimev1pb.ConversationRequestAlpha2{
				Name:       componentName,
				ToolChoice: ptr.Of("required"),
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{{
						MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
							OfUser: &runtimev1pb.ConversationMessageOfUser{
								Content: []*runtimev1pb.ConversationMessageContent{{Text: "hello"}},
							},
						},
					}},
				}},
			},
			expectedReason: "tool choice must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used",
		},
		"tool choice name not found": {
			req: &runtimev1pb.ConversationRequestAlpha2{
				Name:       componentName,
				ToolChoice: ptr.Of("missing_tool"),
				Tools: []*runtimev1pb.ConversationTools{{
					ToolTypes: &runtimev1pb.ConversationTools_Function{
						Function: &runtimev1pb.ConversationToolsFunction{Name: "other_tool"},
					},
				}},
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{{
						MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
							OfUser: &runtimev1pb.ConversationMessageOfUser{
								Content: []*runtimev1pb.ConversationMessageContent{{Text: "hello"}},
							},
						},
					}},
				}},
			},
			expectedReason: "tool choice selected was not found. Must be 'auto', 'none', 'required', or a specific tool name matching the tools available to be used",
		},
		"empty reasoning effort": {
			req: &runtimev1pb.ConversationRequestAlpha2{
				Name:            componentName,
				ReasoningEffort: ptr.Of(""),
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{{
						MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
							OfUser: &runtimev1pb.ConversationMessageOfUser{
								Content: []*runtimev1pb.ConversationMessageContent{{Text: "hello"}},
							},
						},
					}},
				}},
			},
			expectedReason: "reasoning_effort must not be empty when set",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := fakeAPI.ConverseAlpha2(t.Context(), tc.req)
			require.Error(t, err)
			require.ErrorIs(t, err, messages.ErrConversationInvalidParams)

			var apiErr messages.APIError
			require.ErrorAs(t, err, &apiErr)
			msg := apiErr.Message()
			require.NotContains(t, msg, "%!", "error message must not leak an fmt error verb such as %%!(EXTRA ...): %q", msg)
			require.Contains(t, msg, componentName)
			require.Contains(t, msg, tc.expectedReason)
		})
	}
}

// TestConverseAlpha2Reasoning asserts that the reasoning effort and the
// reasoning sent back on assistant messages reach the component, and that the
// reasoning the component returns reaches the caller, scrubbed like the content.
// The echo component reflects both inputs in the reasoning it returns.
func TestConverseAlpha2Reasoning(t *testing.T) {
	const componentName = "test-echo"

	compStore := compstore.New()
	compStore.AddConversation(componentName, echo.NewEcho(testLogger))

	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	user := &runtimev1pb.ConversationMessage{
		MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
			OfUser: &runtimev1pb.ConversationMessageOfUser{
				Content: []*runtimev1pb.ConversationMessageContent{{Text: "hello"}},
			},
		},
	}
	assistantWithReasoning := func(reasoning string) *runtimev1pb.ConversationMessage {
		return &runtimev1pb.ConversationMessage{
			MessageTypes: &runtimev1pb.ConversationMessage_OfAssistant{
				OfAssistant: &runtimev1pb.ConversationMessageOfAssistant{
					Content:   []*runtimev1pb.ConversationMessageContent{{Text: "hi"}},
					Reasoning: ptr.Of(reasoning),
				},
			},
		}
	}

	tests := map[string]struct {
		effort         *string
		messages       []*runtimev1pb.ConversationMessage
		scrubInput     bool
		scrubOutput    bool
		expectedResult *string
	}{
		"no reasoning returns no reasoning": {
			messages:       []*runtimev1pb.ConversationMessage{user},
			expectedResult: nil,
		},
		"reasoning effort reaches the component": {
			effort:         ptr.Of("high"),
			messages:       []*runtimev1pb.ConversationMessage{user},
			expectedResult: ptr.Of("reasoning effort: high"),
		},
		"reasoning on an assistant message reaches the component": {
			messages:       []*runtimev1pb.ConversationMessage{user, assistantWithReasoning("earlier thought")},
			expectedResult: ptr.Of("earlier thought"),
		},
		"empty reasoning on an assistant message is not sent": {
			messages:       []*runtimev1pb.ConversationMessage{user, assistantWithReasoning("")},
			expectedResult: nil,
		},
		"reasoning sent back is scrubbed when the input is": {
			messages:       []*runtimev1pb.ConversationMessage{user, assistantWithReasoning("my email is test@example.com")},
			scrubInput:     true,
			expectedResult: ptr.Of("my email is <EMAIL_ADDRESS>"),
		},
		"returned reasoning is scrubbed when the output is": {
			messages:       []*runtimev1pb.ConversationMessage{user, assistantWithReasoning("my email is test@example.com")},
			scrubOutput:    true,
			expectedResult: ptr.Of("my email is <EMAIL_ADDRESS>"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resp, err := fakeAPI.ConverseAlpha2(t.Context(), &runtimev1pb.ConversationRequestAlpha2{
				Name:            componentName,
				ReasoningEffort: tc.effort,
				ScrubPii:        ptr.Of(tc.scrubOutput),
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: tc.messages,
					ScrubPii: ptr.Of(tc.scrubInput),
				}},
			})
			require.NoError(t, err)
			require.Len(t, resp.GetOutputs(), 1)
			require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)

			message := resp.GetOutputs()[0].GetChoices()[0].GetMessage()
			require.Equal(t, tc.expectedResult, message.Reasoning)
			require.NotContains(t, message.GetContent(), "earlier thought", "reasoning must not leak into the content")
		})
	}
}
