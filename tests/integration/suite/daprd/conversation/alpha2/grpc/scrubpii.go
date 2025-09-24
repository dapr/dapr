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
)

func init() {
	suite.Register(new(scrubpii))
}

type scrubpii struct {
	daprd *daprd.Daprd
}

func (s *scrubpii) Setup(t *testing.T) []framework.Option {
	s.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey
`))

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *scrubpii) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	t.Run("scrub input phone number", func(t *testing.T) {
		scrubInput := true

		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there, my phone number is +2222222222",
										},
									},
								},
							},
						},
					},
					ScrubPii: &scrubInput,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there, my phone number is <PHONE_NUMBER>", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})

	t.Run("scrub input great phone number", func(t *testing.T) {
		scrubInput := true
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there, my phone number is +4422222222",
										},
									},
								},
							},
						},
					},
					ScrubPii: &scrubInput,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there, my phone number is <PHONE_NUMBER>", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})

	t.Run("scrub input email", func(t *testing.T) {
		scrubInput := true

		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there, my email is test@test.com",
										},
									},
								},
							},
						},
					},
					ScrubPii: &scrubInput,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there, my email is <EMAIL_ADDRESS>", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})

	t.Run("scrub input ip address", func(t *testing.T) {
		scrubInput := true

		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there from 10.8.9.1",
										},
									},
								},
							},
						},
					},
					ScrubPii: &scrubInput,
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there from <IP>", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})

	t.Run("scrub all outputs for PII", func(t *testing.T) {
		scrubOutput := true

		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there from 10.8.9.1",
										},
									},
								},
							},
						},
					},
				},
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there, my email is test@test.com",
										},
									},
								},
							},
						},
					},
				},
			},
			ScrubPii: &scrubOutput,
		})

		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there from <IP>\nwell hello there, my email is <EMAIL_ADDRESS>", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})

	t.Run("no scrubbing on good input", func(t *testing.T) {
		scrubOutput := true

		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there",
										},
									},
								},
							},
						},
					},
					ScrubPii: &scrubOutput,
				},
			},
			ScrubPii: &scrubOutput,
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		require.Equal(t, "well hello there", resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent())
	})
}
