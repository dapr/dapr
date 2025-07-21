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
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
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

	t.Run("all fields", func(t *testing.T) {
		tool := &rtv1.ConversationTools{
			ToolTypes: &rtv1.ConversationTools_Function{
				Function: &rtv1.ConversationToolsFunction{
					Name:        &wrapperspb.StringValue{Value: "test_function"},
					Description: &wrapperspb.StringValue{Value: "A test function"},
					Parameters: map[string]*anypb.Any{
						"param1": &anypb.Any{Value: []byte(`"string"`)},
					},
				},
			},
		}

		parameters := map[string]*anypb.Any{
			"max_tokens": &anypb.Any{Value: []byte(`100`)},
			"model":      &anypb.Any{Value: []byte(`"test-model"`)},
		}
		metadata := map[string]string{
			"api_key": "test-key",
			"version": "1.0",
		}

		resp, err := client.ConverseV1Alpha2(ctx, &rtv1.ConversationRequestV1Alpha2{
			Name:      "test-alpha2-echo",
			ContextId: ptr.Of("test-conversation-123"),
			// multiple inputs
			Inputs: []*rtv1.ConversationInputV1Alpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: &wrapperspb.StringValue{Value: "test-user"},
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
					ScrubPII: ptr.Of(false),
				},
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: &wrapperspb.StringValue{Value: "test-system"},
									Content: []*rtv1.ConversationContentMessageContent{
										{
											Text: &wrapperspb.StringValue{
												Value: "You are a helpful assistant",
											},
										},
									},
								},
							},
						},
					},
					ScrubPII: ptr.Of(true),
				},
			},
			Parameters:  parameters,
			Metadata:    metadata,
			ScrubPii:    ptr.Of(true),
			Temperature: ptr.Of(0.7),
			Tools:       []*rtv1.ConversationTools{tool},
			ToolChoice:  &wrapperspb.StringValue{Value: "auto"},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 2) // Should have outputs for both inputs
		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Equal(t, "well hello there", resp.GetOutputs()[0].GetChoices().GetMessage().GetValue())
		require.NotNil(t, resp.GetOutputs()[1].GetChoices())
		require.Equal(t, "You are a helpful assistant", resp.GetOutputs()[1].GetChoices().GetMessage().GetValue())
	})
}
