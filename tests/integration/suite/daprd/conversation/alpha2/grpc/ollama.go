/*
Copyright 2026 The Dapr Authors
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
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(ollamaTools))
}

// ollamaTools validates that tool calling works end-to-end with the Conversation API using Ollama Conversation component.
type ollamaTools struct {
	daprd *daprd.Daprd
}

func (o *ollamaTools) Setup(t *testing.T) []framework.Option {
	if os.Getenv("OLLAMA_ENABLED") == "" {
		t.Skip("Skipping Ollama conversation integration test: requires local Ollama server (set OLLAMA_ENABLED=1 to enable)")
	}

	o.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: test-alpha2-ollama
spec:
  type: conversation.ollama
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(o.daprd),
	}
}

func (o *ollamaTools) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	client := o.daprd.GRPCClient(t, ctx)

	toolParameters, err := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_link": map[string]any{
				"type":        "string",
				"description": "The repository link",
			},
		},
		"required": []any{"repo_link"},
	})
	require.NoError(t, err)

	tool := &rtv1.ConversationTools{
		ToolTypes: &rtv1.ConversationTools_Function{
			Function: &rtv1.ConversationToolsFunction{
				Name:        "get_project_name",
				Description: nil,
				Parameters:  toolParameters,
			},
		},
	}

	req := &rtv1.ConversationRequestAlpha2{
		Name: "test-ollama-tools",
		Inputs: []*rtv1.ConversationInputAlpha2{
			{
				Messages: []*rtv1.ConversationMessage{
					{
						MessageTypes: &rtv1.ConversationMessage_OfUser{
							OfUser: &rtv1.ConversationMessageOfUser{
								Content: []*rtv1.ConversationMessageContent{
									{
										Text: "What is this open source project called? Use the tool if needed.",
									},
								},
							},
						},
					},
				},
			},
		},
		Tools:      []*rtv1.ConversationTools{tool},
		ToolChoice: ptr.Of("required"), // require tool calls to ensure we get appropriate tool call results
	}

	resp, err := client.ConverseAlpha2(ctx, req)
	require.NoError(t, err)
	t.Logf("raw ConversationResponseAlpha2 from Ollama:\n%s", resp.String())

	require.NotEmpty(t, resp.GetOutputs())
	require.NotEmpty(t, resp.GetOutputs()[0].GetChoices())

	// We consider tool calling to be working if Ollama returns at least one tool call,
	// with the expected function name and arguments shape.
	// However, models can answer directly without calling tools...
	foundToolCall := false

	for oi, output := range resp.GetOutputs() {
		t.Logf("output[%d] stop_reason=%q model=%v usage=%+v", oi, output.GetChoices()[0].GetFinishReason(), output.GetModel(), output.GetUsage())

		for ci, choice := range output.GetChoices() {
			msg := choice.GetMessage()
			if msg == nil {
				t.Logf("output[%d].choice[%d]: nil message", oi, ci)
				continue
			}

			t.Logf("output[%d].choice[%d]: finish_reason=%q index=%d content=%q tool_calls_len=%d",
				oi, ci, choice.GetFinishReason(), choice.GetIndex(), msg.GetContent(), len(msg.GetToolCalls()))

			if len(msg.GetToolCalls()) == 0 {
				if msg.GetContent() != "" {
					t.Logf("output[%d].choice[%d]: no tool calls, content=%q", oi, ci, msg.GetContent())
				}
				continue
			}

			for ti, tc := range msg.GetToolCalls() {
				fn := tc.GetFunction()
				if tc.Id != nil {
					t.Logf("output[%d].choice[%d].tool_call[%d]: id=%q name=%q args=%s",
						oi, ci, ti, *tc.Id, fn.GetName(), fn.GetArguments())
				}
			}

			foundToolCall = true
			toolCall := msg.GetToolCalls()[0]
			require.Equal(t, "get_project_name", toolCall.GetFunction().GetName())
			require.Contains(t, toolCall.GetFunction().GetArguments(), "repo_link")
			break
		}
		if foundToolCall {
			break
		}
	}

	if !foundToolCall {
		content := resp.GetOutputs()[0].GetChoices()[0].GetMessage().GetContent()
		t.Logf("no tool calls detected; first choice content=%q", content)
		require.Fail(t, "expected at least one tool call when tool_choice is 'required', but none were returned")
	}
}
