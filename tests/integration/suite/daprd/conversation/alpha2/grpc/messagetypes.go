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
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(messagetypes))
}

type messagetypes struct {
	daprd *daprd.Daprd
}

func (m *messagetypes) Setup(t *testing.T) []framework.Option {
	m.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: test-alpha2-echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey
`))

	return []framework.Option{
		framework.WithProcesses(m.daprd),
	}
}

func (m *messagetypes) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	client := m.daprd.GRPCClient(t, ctx)

	// Test all message types
	t.Run("of_user", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: ptr.Of("user name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "user message",
										},
									},
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices.GetFinishReason())
		require.Equal(t, int64(0), choices.GetIndex())
		require.NotNil(t, choices.GetMessage())
		require.Equal(t, "user message", choices.GetMessage().GetContent())
		// Test that refusal, toolCalls fields are present but not populated for echo
		require.Empty(t, choices.GetMessage().GetRefusal())
		require.Empty(t, choices.GetMessage().GetToolCalls())
	})

	t.Run("of_system", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: ptr.Of("system name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "system message",
										},
									},
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices.GetFinishReason())
		require.Equal(t, int64(0), choices.GetIndex())
		require.NotNil(t, choices.GetMessage())
		require.Equal(t, "system message", choices.GetMessage().GetContent())
		require.Empty(t, choices.GetMessage().GetRefusal())
		require.Empty(t, choices.GetMessage().GetToolCalls())
	})

	t.Run("of_developer", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfDeveloper{
								OfDeveloper: &rtv1.ConversationMessageOfDeveloper{
									Name: ptr.Of("dev name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "developer message",
										},
									},
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices.GetFinishReason())
		require.Equal(t, int64(0), choices.GetIndex())
		require.NotNil(t, choices.GetMessage())
		require.Equal(t, "developer message", choices.GetMessage().GetContent())
		require.Empty(t, choices.GetMessage().GetRefusal())
		require.Empty(t, choices.GetMessage().GetToolCalls())
	})

	t.Run("of_assistant", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfAssistant{
								OfAssistant: &rtv1.ConversationMessageOfAssistant{
									Name: ptr.Of("assistant name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "assistant message",
										},
									},
									ToolCalls: []*rtv1.ConversationToolCalls{
										{
											Id: ptr.Of("call_123"),
											ToolTypes: &rtv1.ConversationToolCalls_Function{
												Function: &rtv1.ConversationToolCallsOfFunction{
													Name: "test_function",
													Arguments: map[string]*anypb.Any{
														"arg1": &anypb.Any{Value: []byte(`"value1"`)},
													},
												},
											},
										},
									},
									Refusal: ptr.Of("I cannot help with that request"),
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		// Echo component returns separate outputs for content, refusal, and tool calls
		require.Len(t, resp.GetOutputs(), 3)

		// @sicoyle I think this should be one output
		// First output: content
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices0 := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices0.GetFinishReason())
		require.Equal(t, int64(0), choices0.GetIndex())
		require.NotNil(t, choices0.GetMessage())
		require.Equal(t, "assistant message", choices0.GetMessage().GetContent())
		require.Empty(t, choices0.GetMessage().GetRefusal())
		require.Empty(t, choices0.GetMessage().GetToolCalls())

		// Second output: refusal
		require.NotNil(t, resp.GetOutputs()[1].GetChoices())
		require.Len(t, resp.GetOutputs()[1].GetChoices(), 1)
		choices1 := resp.GetOutputs()[1].GetChoices()[0]
		require.Equal(t, "stop", choices1.GetFinishReason())
		require.Equal(t, int64(0), choices1.GetIndex())
		require.NotNil(t, choices1.GetMessage())
		require.Equal(t, "I cannot help with that request", choices1.GetMessage().GetContent())
		require.Empty(t, choices1.GetMessage().GetRefusal())
		require.Empty(t, choices1.GetMessage().GetToolCalls())

		// Third output: tool call
		require.NotNil(t, resp.GetOutputs()[2].GetChoices())
		require.Len(t, resp.GetOutputs()[2].GetChoices(), 1)
		choices2 := resp.GetOutputs()[2].GetChoices()[0]
		require.Equal(t, "stop", choices2.GetFinishReason())
		require.Equal(t, int64(0), choices2.GetIndex())
		require.NotNil(t, choices2.GetMessage())
		require.Contains(t, choices2.GetMessage().GetContent(), "test_function")
		require.Empty(t, choices2.GetMessage().GetRefusal())
		require.Empty(t, choices2.GetMessage().GetToolCalls())
	})

	t.Run("of_tool", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfTool{
								OfTool: &rtv1.ConversationMessageOfTool{
									ToolId: ptr.Of("tool-123"),
									Name:   "tool name",
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "tool message",
										},
									},
								},
							},
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices.GetFinishReason())
		require.Equal(t, int64(0), choices.GetIndex())
		require.NotNil(t, choices.GetMessage())
		require.Equal(t, "tool message", choices.GetMessage().GetContent())
		require.Empty(t, choices.GetMessage().GetRefusal())
		require.Empty(t, choices.GetMessage().GetToolCalls())
	})

	t.Run("multiple messages in conversation", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: ptr.Of("user-1"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "first user message",
										},
									},
								},
							},
						},
						{
							MessageTypes: &rtv1.ConversationMessage_OfAssistant{
								OfAssistant: &rtv1.ConversationMessageOfAssistant{
									Name: ptr.Of("assistant-1"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "first assistant response",
										},
									},
								},
							},
						},
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: ptr.Of("user-2"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "second user message",
										},
									},
								},
							},
						},
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: ptr.Of("system-1"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "system instruction",
										},
									},
								},
							},
						},
					},
					ScrubPii: ptr.Of(false),
				},
			},
		})
		require.NoError(t, err)
		// Echo component returns separate outputs for each message
		require.Len(t, resp.GetOutputs(), 4)

		// First output: first user message
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices0 := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices0.GetFinishReason())
		require.Equal(t, int64(0), choices0.GetIndex())
		require.NotNil(t, choices0.GetMessage())
		require.Equal(t, "first user message", choices0.GetMessage().GetContent())
		require.Empty(t, choices0.GetMessage().GetRefusal())
		require.Empty(t, choices0.GetMessage().GetToolCalls())

		// Second output: first assistant response
		require.NotNil(t, resp.GetOutputs()[1].GetChoices())
		require.Len(t, resp.GetOutputs()[1].GetChoices(), 1)
		choices1 := resp.GetOutputs()[1].GetChoices()[0]
		require.Equal(t, "stop", choices1.GetFinishReason())
		require.Equal(t, int64(1), choices1.GetIndex())
		require.NotNil(t, choices1.GetMessage())
		require.Equal(t, "first assistant response", choices1.GetMessage().GetContent())
		require.Empty(t, choices1.GetMessage().GetRefusal())
		require.Empty(t, choices1.GetMessage().GetToolCalls())

		// Third output: second user message
		require.NotNil(t, resp.GetOutputs()[2].GetChoices())
		require.Len(t, resp.GetOutputs()[2].GetChoices(), 1)
		choices2 := resp.GetOutputs()[2].GetChoices()[0]
		require.Equal(t, "stop", choices2.GetFinishReason())
		require.Equal(t, int64(2), choices2.GetIndex())
		require.NotNil(t, choices2.GetMessage())
		require.Equal(t, "second user message", choices2.GetMessage().GetContent())
		require.Empty(t, choices2.GetMessage().GetRefusal())
		require.Empty(t, choices2.GetMessage().GetToolCalls())

		// Fourth output: system instruction
		require.NotNil(t, resp.GetOutputs()[3].GetChoices())
		require.Len(t, resp.GetOutputs()[3].GetChoices(), 1)
		choices3 := resp.GetOutputs()[3].GetChoices()[0]
		require.Equal(t, "stop", choices3.GetFinishReason())
		require.Equal(t, int64(3), choices3.GetIndex())
		require.NotNil(t, choices3.GetMessage())
		require.Equal(t, "system instruction", choices3.GetMessage().GetContent())
		require.Empty(t, choices3.GetMessage().GetRefusal())
		require.Empty(t, choices3.GetMessage().GetToolCalls())
	})
}
