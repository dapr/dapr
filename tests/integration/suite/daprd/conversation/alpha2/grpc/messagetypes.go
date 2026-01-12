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
		// Test that toolCalls field is present but not populated for echo
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
													Name:      "test_function",
													Arguments: "test-string",
												},
											},
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
		// Echo component returns the assistant message with tool calls
		require.Len(t, resp.GetOutputs(), 1)

		// assistant message with tool calls
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices0 := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "tool_calls", choices0.GetFinishReason())
		require.Equal(t, int64(0), choices0.GetIndex())
		require.NotNil(t, choices0.GetMessage())
		require.Equal(t, "assistant message", choices0.GetMessage().GetContent())
		require.NotEmpty(t, choices0.GetMessage().GetToolCalls())
		require.Equal(t, "call_123", choices0.GetMessage().GetToolCalls()[0].GetId())
		require.Equal(t, "test_function", choices0.GetMessage().GetToolCalls()[0].GetFunction().GetName())
		require.Equal(t, "test-string", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetToolCalls()[0].GetFunction().GetArguments())
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
		require.Equal(t, "Tool Response for tool ID 'tool-123' with name 'tool name': tool message", choices.GetMessage().GetContent())
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
		// Echo component combines multiple messages into a single output
		require.Len(t, resp.GetOutputs(), 1)

		// Single output with combined content
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices0 := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "stop", choices0.GetFinishReason())
		require.Equal(t, int64(0), choices0.GetIndex())
		require.NotNil(t, choices0.GetMessage())
		require.Equal(t, "first user message\nfirst assistant response\nsecond user message\nsystem instruction", choices0.GetMessage().GetContent())
		require.Empty(t, choices0.GetMessage().GetToolCalls())
	})
}
