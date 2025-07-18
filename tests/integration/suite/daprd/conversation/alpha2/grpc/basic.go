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
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t, daprd.WithResourceFiles(`
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
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	t.Run("good input", func(t *testing.T) {

		/*
			types:
			*ConversationMessage_OfDeveloper
			*ConversationMessage_OfSystem
			*ConversationMessage_OfUser
			*ConversationMessage_OfAssistant
			*ConversationMessage_OfTool
		*/
		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: nil,
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "well hello there",
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
		require.Equal(t, "well hello there", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
	})
}
