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
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
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
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: &wrapperspb.StringValue{Value: "user name"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "user message",
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
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "user message", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})

	t.Run("of_system", func(t *testing.T) {
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: &wrapperspb.StringValue{Value: "system name"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "system message",
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
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "system message", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})

	t.Run("of_developer", func(t *testing.T) {
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfDeveloper{
								OfDeveloper: &rtv1.ConversationMessageOfDeveloper{
									Name: &wrapperspb.StringValue{Value: "dev name"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "developer message",
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
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "developer message", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})

	t.Run("of_assistant", func(t *testing.T) {
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfAssistant{
								OfAssistant: &rtv1.ConversationMessageOfAssistant{
									Name: &wrapperspb.StringValue{Value: "assistant name"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "assistant message",
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
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "assistant message", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})

	t.Run("of_tool", func(t *testing.T) {
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfTool{
								OfTool: &rtv1.ConversationMessageOfTool{
									ToolId: &wrapperspb.StringValue{Value: "tool-123"},
									Name:   &wrapperspb.StringValue{Value: "tool name"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "tool message",
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
		require.Len(t, resp.GetOutputs(), 1)
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "tool message", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})
}
