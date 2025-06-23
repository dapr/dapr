/*
Copyright 2024 The Dapr Authors
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

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(simpleDebug))
}

type simpleDebug struct {
	daprd *daprd.Daprd
}

func (sd *simpleDebug) Setup(t *testing.T) []framework.Option {
	sd.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: model
    value: "echo-model"
`))

	return []framework.Option{
		framework.WithProcesses(sd.daprd),
	}
}

func (sd *simpleDebug) Run(t *testing.T, ctx context.Context) {
	sd.daprd.WaitUntilRunning(t, ctx)

	client := sd.daprd.GRPCClient(t, ctx)

	t.Run("test weather query with tools", func(t *testing.T) {
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Content: "What's the weather like in San Francisco?",
					Role:    func(s string) *string { return &s }("user"),
					Tools: []*runtimev1pb.Tool{
						{
							Type: "function",
							Function: &runtimev1pb.ToolFunction{
								Name:        "get_weather",
								Description: "Get current weather for a location",
								Parameters:  `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`,
							},
						},
					},
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.GetOutputs(), 1)

		output := resp.GetOutputs()[0]
		t.Logf("Response Result: %s", output.GetResult())
		t.Logf("Response ToolCalls: %v", output.GetToolCalls())
		t.Logf("Response FinishReason: %s", output.GetFinishReason())
		t.Logf("Number of ToolCalls: %d", len(output.GetToolCalls()))

		// The echo component should detect "weather" keyword and return tool calls
		// Let's log what we're actually getting to debug
		if len(output.GetToolCalls()) > 0 {
			t.Logf("✅ Tool calling works! First tool call: %v", output.GetToolCalls()[0])
		} else {
			t.Logf("❌ No tool calls returned. This suggests the tool calling flow isn't working as expected.")
			// Still check what we got
			if output.GetResult() == "What's the weather like in San Francisco?" {
				t.Logf("🔍 Got regular echo response, which means tool calling detection might not be working")
			} else {
				t.Logf("🔍 Got unexpected response: %s", output.GetResult())
			}
		}
	})

	t.Run("test regular message without tools", func(t *testing.T) {
		req := &runtimev1pb.ConversationRequest{
			Name: "echo",
			Inputs: []*runtimev1pb.ConversationInput{
				{
					Content: "Hello world",
					Role:    func(s string) *string { return &s }("user"),
				},
			},
		}

		resp, err := client.ConverseAlpha1(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.GetOutputs(), 1)

		output := resp.GetOutputs()[0]
		t.Logf("Regular Response Result: %s", output.GetResult())
		t.Logf("Regular Response ToolCalls: %v", output.GetToolCalls())

		// This should work as normal echo
		require.Equal(t, "Hello world", output.GetResult())
		require.Empty(t, output.GetToolCalls())
	})
}
