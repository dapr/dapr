package universal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"

	"github.com/dapr/components-contrib/conversation"
	"github.com/dapr/components-contrib/conversation/echo"
	"github.com/dapr/components-contrib/conversation/openai"
	"github.com/dapr/components-contrib/metadata"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/ptr"
)

func TestConverseAlpha2OpenAIToolMessageContent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content []*runtimev1pb.ConversationMessageContent
		want    string
	}{
		{
			name:    "single part",
			content: []*runtimev1pb.ConversationMessageContent{{Text: "first part"}},
			want:    "first part",
		},
		{
			name:    "multiple parts",
			content: []*runtimev1pb.ConversationMessageContent{{Text: "first part"}, {Text: "second part"}},
			want:    "first part\nsecond part",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read completion request: %v", err)
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
			}))
			t.Cleanup(server.Close)

			component := openai.NewOpenAI(testLogger)
			require.NoError(t, component.Init(t.Context(), conversation.Metadata{
				Base: metadata.Base{Properties: map[string]string{
					"key":      "test-key",
					"endpoint": server.URL,
					"model":    "gpt-4o-mini",
				}},
			}))
			t.Cleanup(func() { require.NoError(t, component.Close()) })
			store := compstore.New()
			store.AddConversation("openai", component)
			api := &Universal{
				logger:     testLogger,
				resiliency: resiliency.New(nil),
				compStore:  store,
			}

			response, err := api.ConverseAlpha2(t.Context(), &runtimev1pb.ConversationRequestAlpha2{
				Name: "openai",
				Inputs: []*runtimev1pb.ConversationInputAlpha2{{
					Messages: []*runtimev1pb.ConversationMessage{
						{MessageTypes: &runtimev1pb.ConversationMessage_OfUser{
							OfUser: &runtimev1pb.ConversationMessageOfUser{
								Content: []*runtimev1pb.ConversationMessageContent{{Text: "look up the result"}},
							},
						}},
						{MessageTypes: &runtimev1pb.ConversationMessage_OfAssistant{
							OfAssistant: &runtimev1pb.ConversationMessageOfAssistant{
								ToolCalls: []*runtimev1pb.ConversationToolCalls{{
									Id: ptr.Of("call-1"),
									ToolTypes: &runtimev1pb.ConversationToolCalls_Function{
										Function: &runtimev1pb.ConversationToolCallsOfFunction{Name: "lookup", Arguments: "{}"},
									},
								}},
							},
						}},
						{MessageTypes: &runtimev1pb.ConversationMessage_OfTool{
							OfTool: &runtimev1pb.ConversationMessageOfTool{
								ToolId:  ptr.Of("call-1"),
								Name:    "lookup",
								Content: tc.content,
							},
						}},
					},
				}},
			})
			require.NoError(t, err, "outbound completion requests: %d", len(requests))
			require.Len(t, requests, 1)
			var request struct {
				Messages []map[string]any `json:"messages"`
			}
			require.NoError(t, json.Unmarshal(<-requests, &request))
			require.Len(t, request.Messages, 3)
			require.Equal(t, "tool", request.Messages[2]["role"])
			require.Equal(t, "call-1", request.Messages[2]["tool_call_id"])
			require.Equal(t, tc.want, request.Messages[2]["content"])
			require.Len(t, response.GetOutputs(), 1)
			require.Len(t, response.GetOutputs()[0].GetChoices(), 1)
			require.Equal(t, "done", response.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
		})
	}
}

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
