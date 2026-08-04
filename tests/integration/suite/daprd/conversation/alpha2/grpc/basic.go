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
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
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
`, `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: test-alpha2-echo-maxtokens
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey
  - name: maxTokens
    value: "2"
`))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	t.Run("all fields", func(t *testing.T) {
		toolParameters, err := structpb.NewStruct(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"param1": map[string]any{
					"type":        "string",
					"description": "A test parameter",
				},
			},
			"required": []any{"param1"},
		})
		require.NoError(t, err)

		tool := &rtv1.ConversationTools{
			ToolTypes: &rtv1.ConversationTools_Function{
				Function: &rtv1.ConversationToolsFunction{
					Name:        "test_function",
					Description: new("A test function"),
					Parameters:  toolParameters,
				},
			},
		}

		// max_tokens is a first-class field now; parameters only carries the
		// legacy pass-through entry until the field is removed at stable.
		modelParam, err := anypb.New(wrapperspb.String("test-model"))
		require.NoError(t, err)
		parameters := map[string]*anypb.Any{
			"model": modelParam,
		}
		metadata := map[string]string{
			"api_key": "test-key",
			"version": "1.0",
		}

		contextID := "test-conversation-123"
		responseFormat, err := structpb.NewStruct(map[string]any{
			"type": "object",
			"properties": map[string]any{
				"result": map[string]any{
					"type": "string",
				},
			},
			"required": []any{"result"},
		})
		require.NoError(t, err)
		cacheRetention := durationpb.New(24 * time.Hour)
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name:      "test-alpha2-echo",
			ContextId: new(contextID),
			// multiple inputs
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Name: new("test-user"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "well hello there",
										},
									},
								},
							},
						},
					},
					ScrubPii: new(false),
				},
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfSystem{
								OfSystem: &rtv1.ConversationMessageOfSystem{
									Name: new("test-system"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "You are a helpful assistant",
										},
									},
								},
							},
						},
					},
					ScrubPii: new(true),
				},
			},
			Parameters:           parameters,
			Metadata:             metadata,
			ScrubPii:             new(true),
			Temperature:          new(0.7),
			MaxTokens:            new(int64(100)),
			Tools:                []*rtv1.ConversationTools{tool},
			ToolChoice:           new("auto"),
			ResponseFormat:       responseFormat,
			PromptCacheRetention: cacheRetention,
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
		require.NotNil(t, resp.GetOutputs()[0].GetUsage())
		require.Equal(t, uint64(8), resp.GetOutputs()[0].GetUsage().GetCompletionTokens())
		require.Equal(t, uint64(8), resp.GetOutputs()[0].GetUsage().GetPromptTokens())
		require.Equal(t, uint64(16), resp.GetOutputs()[0].GetUsage().GetTotalTokens())
	})

	t.Run("max tokens truncates output", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "one two three four five",
										},
									},
								},
							},
						},
					},
				},
			},
			MaxTokens: new(int64(2)),
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choice := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "one two", choice.GetMessage().GetContent())
		require.Equal(t, "length", choice.GetFinishReason())
		require.NotNil(t, resp.GetOutputs()[0].GetUsage())
		require.Equal(t, uint64(2), resp.GetOutputs()[0].GetUsage().GetCompletionTokens())
		require.Equal(t, uint64(5), resp.GetOutputs()[0].GetUsage().GetPromptTokens())
		require.Equal(t, uint64(7), resp.GetOutputs()[0].GetUsage().GetTotalTokens())
	})

	t.Run("max tokens metadata default applies", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo-maxtokens",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "one two three four five",
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
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choice := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "one two", choice.GetMessage().GetContent())
		require.Equal(t, "length", choice.GetFinishReason())
	})

	t.Run("request max tokens overrides metadata default", func(t *testing.T) {
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo-maxtokens",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "one two three four five",
										},
									},
								},
							},
						},
					},
				},
			},
			MaxTokens: new(int64(100)),
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Len(t, resp.GetOutputs()[0].GetChoices(), 1)
		choice := resp.GetOutputs()[0].GetChoices()[0]
		require.Equal(t, "one two three four five", choice.GetMessage().GetContent())
		require.Equal(t, "stop", choice.GetFinishReason())
	})

	t.Run("max tokens must be positive", func(t *testing.T) {
		_, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "hello",
										},
									},
								},
							},
						},
					},
				},
			},
			MaxTokens: new(int64(0)),
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("max tokens must fit in int32", func(t *testing.T) {
		_, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name: "test-alpha2-echo",
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "hello",
										},
									},
								},
							},
						},
					},
				},
			},
			// math.MaxInt32 + 1: would wrap to a negative int on 32-bit builds
			// if it reached a component, so the API rejects it up front.
			MaxTokens: new(int64(2147483648)),
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
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
									Name: new("assistant name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "assistant message",
										},
									},
									ToolCalls: []*rtv1.ConversationToolCalls{
										{
											Id: new("id 123"),
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
									Name: new("assistant name"),
									Content: []*rtv1.ConversationMessageContent{
										{
											Text: "assistant message",
										},
									},
									ToolCalls: []*rtv1.ConversationToolCalls{
										{
											Id: new("call_123"),
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
