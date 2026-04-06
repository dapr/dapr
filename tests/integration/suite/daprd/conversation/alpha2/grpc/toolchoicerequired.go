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
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(toolchoicerequired))
}

// toolchoicerequired tests end-to-end behavior when tool_choice=required is set.
// It verifies:
//   - The echo component honours tool_choice=required by returning tool calls (happy path).
//   - The Dapr runtime validates that tool_choice=required cannot be used without tools.
//   - A resiliency retry policy is wired to the conversation component so that
//     transient failures (e.g. an LLM that ignores tools and returns empty content)
//     would be retried.  The retry itself is exercised at the unit-test level in
//     pkg/api/universal/conversation_test.go; here we confirm the policy loads and
//     the happy path still succeeds with it in place.
type toolchoicerequired struct {
	daprd *daprd.Daprd
}

func (tc *toolchoicerequired) Setup(t *testing.T) []framework.Option {
	tc.daprd = daprd.New(t,
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo-tool-required
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: key
    value: testkey
---
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: conversation-resiliency
spec:
  policies:
    retries:
      conv-retry:
        policy: constant
        duration: 10ms
        maxRetries: 3
  targets:
    components:
      echo-tool-required:
        outbound:
          retry: conv-retry
`))

	return []framework.Option{
		framework.WithProcesses(tc.daprd),
	}
}

func (tc *toolchoicerequired) Run(t *testing.T, ctx context.Context) {
	tc.daprd.WaitUntilRunning(t, ctx)

	client := tc.daprd.GRPCClient(t, ctx)

	t.Run("tool_choice=required with tools - echo returns tool calls", func(t *testing.T) {
		// The echo component always produces a ToolCallRequest when tools are provided,
		// which satisfies the tool_choice=required contract.  If the component were an LLM
		// that returned empty content instead, the langchaingokit Converse() helper would
		// return an error and the resiliency runner would retry up to 3 times.
		resp, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name:       "echo-tool-required",
			ToolChoice: new("required"),
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{Text: "identify at-risk orders"},
									},
								},
							},
						},
					},
				},
			},
			Tools: []*rtv1.ConversationTools{
				{
					ToolTypes: &rtv1.ConversationTools_Function{
						Function: &rtv1.ConversationToolsFunction{
							Name:        "GetOrdersAtRiskCount",
							Description: new("Get the count of orders at risk"),
						},
					},
				},
			},
		})
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetOutputs())

		choices := resp.GetOutputs()[0].GetChoices()
		t.Logf("Choices: %v", choices)
		require.NotEmpty(t, choices)

		// echo component must have produced tool calls (satisfying tool_choice=required)
		require.NotEmpty(t, choices[0].GetMessage().GetToolCalls(),
			"expected tool calls in response when tool_choice=required and tools are provided")
		t.Logf("ToolCalls: %v", choices[0].GetMessage().GetToolCalls())
		require.Equal(t, "GetOrdersAtRiskCount", choices[0].GetMessage().GetToolCalls()[0].GetFunction().GetName())
	})

	t.Run("tool_choice=required without tools - returns InvalidArgument", func(t *testing.T) {
		// The Dapr runtime must reject tool_choice=required when no tools are defined.
		_, err := client.ConverseAlpha2(ctx, &rtv1.ConversationRequestAlpha2{
			Name:       "echo-tool-required",
			ToolChoice: new("required"),
			Inputs: []*rtv1.ConversationInputAlpha2{
				{
					Messages: []*rtv1.ConversationMessage{
						{
							MessageTypes: &rtv1.ConversationMessage_OfUser{
								OfUser: &rtv1.ConversationMessageOfUser{
									Content: []*rtv1.ConversationMessageContent{
										{Text: "identify at-risk orders"},
									},
								},
							},
						},
					},
				},
			},
		})
		require.Error(t, err)
		require.Equal(t, codes.InvalidArgument, status.Code(err),
			"expected InvalidArgument when tool_choice=required but no tools provided")
	})
}
