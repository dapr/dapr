package universal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
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

type mockConversationComponent struct {
	conversation.Conversation
	response *conversation.Response
	err      error
}

func (m *mockConversationComponent) Converse(ctx context.Context, request *conversation.Request) (*conversation.Response, error) {
	return m.response, m.err
}

func TestConverseAlpha1_EmptyChoices(t *testing.T) {
	const componentName = "test-mock"
	compStore := compstore.New()
	compStore.AddConversation(componentName, &mockConversationComponent{
		Conversation: echo.NewEcho(testLogger),
		response: &conversation.Response{
			Outputs: []conversation.Result{
				{
					Choices: []conversation.Choice{}, // empty choices slice
				},
			},
		},
	})

	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	req := &runtimev1pb.ConversationRequest{
		Name: componentName,
		Inputs: []*runtimev1pb.ConversationInput{{
			Content: "hello",
			Role:    ptr.Of("user"),
		}},
	}

	resp, err := fakeAPI.ConverseAlpha1(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.GetOutputs(), 1)
	assert.Equal(t, "", resp.GetOutputs()[0].GetResult())
}

func TestConverseAlpha2_NilFunctionCall(t *testing.T) {
	const componentName = "test-mock"
	toolCallID := "call-1"
	compStore := compstore.New()
	compStore.AddConversation(componentName, &mockConversationComponent{
		Conversation: echo.NewEcho(testLogger),
		response: &conversation.Response{
			Outputs: []conversation.Result{
				{
					Choices: []conversation.Choice{
						{
							Message: conversation.Message{
								ToolCallRequest: &[]llms.ToolCall{
									{
										ID:           toolCallID,
										Type:         "custom",
										FunctionCall: nil, // nil function call
									},
								},
							},
						},
					},
				},
			},
		},
	})

	fakeAPI := &Universal{
		logger:     testLogger,
		resiliency: resiliency.New(nil),
		compStore:  compStore,
	}

	req := &runtimev1pb.ConversationRequestAlpha2{
		Name: componentName,
		Inputs: []*runtimev1pb.ConversationInputAlpha2{{
			Messages: []*runtimev1pb.ConversationMessage{{
				MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
					OfUser: &runtimev1pb.ConversationMessageOfUser{
						Content: []*runtimev1pb.ConversationMessageContent{{Text: "hello"}},
					},
				},
			}},
		}},
	}

	resp, err := fakeAPI.ConverseAlpha2(t.Context(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.GetOutputs(), 1)
	require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
	choice := resp.GetOutputs()[0].GetChoices()[0]
	require.Len(t, choice.GetMessage().GetToolCalls(), 1)
	assert.Equal(t, toolCallID, choice.GetMessage().GetToolCalls()[0].GetId())
	assert.Nil(t, choice.GetMessage().GetToolCalls()[0].GetToolTypes())
}
