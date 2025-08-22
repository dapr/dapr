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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

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
					Name:        "test_function",
					Description: ptr.Of("A test function"),
					Parameters: &structpb.Struct{
						Fields: map[string]*structpb.Value{
							"type": {
								Kind: &structpb.Value_StringValue{
									StringValue: "object",
								},
							},
							"properties": {
								Kind: &structpb.Value_StructValue{
									StructValue: &structpb.Struct{
										Fields: map[string]*structpb.Value{
											"param1": {
												Kind: &structpb.Value_StructValue{
													StructValue: &structpb.Struct{
														Fields: map[string]*structpb.Value{
															"type": {
																Kind: &structpb.Value_StringValue{
																	StringValue: "string",
																},
															},
															"description": {
																Kind: &structpb.Value_StringValue{
																	StringValue: "A test parameter",
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
							"required": {
								Kind: &structpb.Value_ListValue{
									ListValue: &structpb.ListValue{
										Values: []*structpb.Value{
											{
												Kind: &structpb.Value_StringValue{
													StringValue: "param1",
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
		}

		parameters := map[string]*anypb.Any{
			"max_tokens": {Value: []byte(`100`)},
			"model":      {Value: []byte(`"test-model"`)},
		}
		metadata := map[string]string{
			"api_key": "test-key",
			"version": "1.0",
		}

		contextID := "test-conversation-123"
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name:      "test-alpha2-echo",
			ContextId: ptr.Of(contextID),
			// multiple inputs
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: ptr.Of("test-user"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there",
										},
									},
								},
							},
						},
					},
					ScrubPii: ptr.Of(false),
				},
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: ptr.Of("test-system"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "You are a helpful assistant",
										},
									},
								},
							},
						},
					},
					ScrubPii: ptr.Of(true),
				},
			},
			Parameters:  parameters,
			Metadata:    metadata,
			ScrubPii:    ptr.Of(true),
			Temperature: ptr.Of(0.7),
			Tools:       []*rtv1.ConversationTools{tool},
			ToolChoice:  ptr.Of("auto"),
		})
		require.NoError(t, err)
		// Echo component returns one output combining all input messages
		require.Len(t, resp.GetOutputs(), 1)
		require.Equal(t, contextID, resp.GetContextId())

		require.NotNil(t, resp.GetOutputs()[0].GetChoices())
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choices0 := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "tool_calls", choices0.GetFinishReason())
		require.Equal(t, int64(0), choices0.GetIndex())
		require.NotNil(t, choices0.GetMessage())
		// echo combines all input messages into one output
		require.Equal(t, "well hello there\nYou are a helpful assistant", choices0.GetMessage().GetContent())
		require.NotEmpty(t, choices0.GetMessage().GetToolCalls())

		toolCalls := choices0.GetMessage().GetToolCalls()
		require.Len(t, toolCalls, 1)
		require.Equal(t, "0", toolCalls[0].GetId())
		require.Equal(t, "test_function", toolCalls[0].GetFunction().GetName())
		require.Equal(t, "param1", toolCalls[0].GetFunction().GetArguments())
	})

	t.Run("invalid json - malformed request", func(t *testing.T) {
		_, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							// This will err
							MessageTypes: nil,
						},
					},
				},
			},
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("correct tool call", func(t *testing.T) {
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
											Id: ptr.Of("id 123"),
											ToolTypes: &rtv1.ConversationToolCalls_Function{
												Function: &rtv1.ConversationToolCallsOfFunction{
													Name:      "test_function",
													Arguments: `{"test": "value"}`,
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
		require.Len(t, resp.GetOutputs(), 1)
		require.JSONEq(t, `{"test": "value"}`, resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetToolCalls()[0].GetFunction().GetArguments())
	})

	t.Run("malformed tool call", func(t *testing.T) {
		_, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
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
											// This should err
											ToolTypes: nil,
										},
									},
								},
							},
						},
					},
				},
			},
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}
