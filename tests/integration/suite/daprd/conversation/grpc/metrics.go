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
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func init() {
	suite.Register(new(metrics))
}

// metrics tests conversation component metrics for both gRPC streaming and non-streaming conversations
type metrics struct {
	daprd *daprd.Daprd
}

func (m *metrics) Setup(t *testing.T) []framework.Option {
	app := app.New(t)

	m.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppID("myapp"),
		daprd.WithResourceFiles(getEchoComponentConfig()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(app, m.daprd),
	}
}

func (m *metrics) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	gclient := m.daprd.GRPCClient(t, ctx)

	t.Run("non-streaming conversation metrics", func(t *testing.T) {
		// Make multiple conversation calls to generate metrics
		for range 3 {
			resp, err := gclient.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
				Name: "echo",
				Inputs: []*rtv1.ConversationInput{
					{
						Content: []*rtv1.ConversationContent{
							{
								ContentType: &rtv1.ConversationContent_Text{
									Text: &rtv1.ConversationText{
										Text: "Hello from metrics test",
									},
								},
							},
						},
						Role: ptr.Of("user"),
					},
				},
			})
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Len(t, resp.GetResults(), 1)
			require.NotNil(t, resp.GetUsage())
		}

		// Wait for metrics to be recorded and verify
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()

			// Verify conversation count metric
			countKey := "dapr_component_conversation_count|app_id:myapp|component:echo|namespace:|success:true|type:non_streaming"
			assert.True(c, metrics[countKey] >= 3, "Expected at least 3 conversation counts, got %v", metrics[countKey])

			// Verify token usage metrics (echo component returns predictable token counts)
			promptTokensKey := "dapr_component_conversation_usage_prompt_tokens|app_id:myapp|component:echo|namespace:|success:true|type:non_streaming"
			completionTokensKey := "dapr_component_conversation_usage_completion_tokens|app_id:myapp|component:echo|namespace:|success:true|type:non_streaming"

			assert.True(c, metrics[promptTokensKey] > 0, "Expected positive prompt tokens, got %v", metrics[promptTokensKey])
			assert.True(c, metrics[completionTokensKey] > 0, "Expected positive completion tokens, got %v", metrics[completionTokensKey])
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("streaming conversation metrics", func(t *testing.T) {
		// Make streaming conversation calls to generate streaming metrics
		for range 2 {
			stream, err := gclient.ConverseStreamAlpha1(ctx, &rtv1.ConversationRequest{
				Name: "echo",
				Inputs: []*rtv1.ConversationInput{
					{
						Content: []*rtv1.ConversationContent{
							{
								ContentType: &rtv1.ConversationContent_Text{
									Text: &rtv1.ConversationText{
										Text: "Streaming test message",
									},
								},
							},
						},
						Role: ptr.Of("user"),
					},
				},
			})
			require.NoError(t, err)

			// Read the stream to completion
			var chunks int
			for {
				chunk, err := stream.Recv()
				if err == io.EOF {
					break
				}
				require.NoError(t, err)
				require.NotNil(t, chunk)
				chunks++
			}
			require.Greater(t, chunks, 0, "Expected to receive streaming chunks")
		}

		// Wait for streaming metrics to be recorded and verify
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()

			// Verify streaming conversation count metric
			streamingCountKey := "dapr_component_conversation_count|app_id:myapp|component:echo|namespace:|success:true|type:streaming"
			assert.True(c, metrics[streamingCountKey] >= 2, "Expected at least 2 streaming conversation counts, got %v", metrics[streamingCountKey])

			// Verify streaming token usage metrics
			streamingPromptTokensKey := "dapr_component_conversation_usage_prompt_tokens|app_id:myapp|component:echo|namespace:|success:true|type:streaming"
			streamingCompletionTokensKey := "dapr_component_conversation_usage_completion_tokens|app_id:myapp|component:echo|namespace:|success:true|type:streaming"

			assert.True(c, metrics[streamingPromptTokensKey] > 0, "Expected positive streaming prompt tokens, got %v", metrics[streamingPromptTokensKey])
			assert.True(c, metrics[streamingCompletionTokensKey] > 0, "Expected positive streaming completion tokens, got %v", metrics[streamingCompletionTokensKey])
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("conversation tool calling metrics", func(t *testing.T) {
		// Test conversation with tool definitions to ensure tool calling metrics
		for range 2 {
			resp, err := gclient.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
				Name: "echo",
				Tools: []*rtv1.ConversationTool{
					{
						Type:        wrapperspb.String("function"),
						Name:        wrapperspb.String("get_weather"),
						Description: wrapperspb.String("Get current weather for a location"),
						Parameters:  wrapperspb.String(`{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`),
					},
				},
				Inputs: []*rtv1.ConversationInput{
					{
						Content: []*rtv1.ConversationContent{
							{
								ContentType: &rtv1.ConversationContent_Text{
									Text: &rtv1.ConversationText{
										Text: "What's the weather like in San Francisco?",
									},
								},
							},
						},
						Role: ptr.Of("user"),
					},
				},
			})
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Len(t, resp.GetResults(), 1)
			require.NotNil(t, resp.GetUsage())

			// Verify the echo component generated a tool call (it does for weather queries)
			output := resp.GetResults()[0]
			if output.GetFinishReason() == "tool_calls" {
				// Tool calling worked - metrics should include this
				require.NotEmpty(t, output.GetContent())
				hasToolCall := false
				for _, part := range output.GetContent() {
					if part.GetToolCall() != nil {
						hasToolCall = true
						break
					}
				}
				assert.True(t, hasToolCall, "Expected tool call in response parts")
			}
		}

		// Verify tool calling metrics are still recorded as regular conversation metrics
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			metrics := m.daprd.Metrics(c, ctx).All()

			// Tool calling conversations should still increment the regular conversation count
			countKey := "dapr_component_conversation_count|app_id:myapp|component:echo|namespace:|success:true|type:non_streaming"
			// Should have previous 3 + these 2 = at least 5 total
			assert.True(c, metrics[countKey] >= 5, "Expected at least 5 total conversation counts including tool calling, got %v", metrics[countKey])
		}, 10*time.Second, 100*time.Millisecond)
	})
}
