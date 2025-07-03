/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/kit/ptr"

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

func getEchoEstimatedTokens(msg ...string) int {
	echoEstimatedTokens := 0
	for _, m := range msg {
		echoEstimatedTokens += len(m) / 4 // Rough estimate of tokens, assuming 4 characters per token
	}
	return echoEstimatedTokens
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t, daprd.WithResourceFiles(getEchoComponentConfig()))

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	getEchoEstimatedTokens := func(msg ...string) int {
		echoEstimatedTokens := 0
		for _, m := range msg {
			echoEstimatedTokens += len(m) / 4 // Rough estimate of tokens, assuming 4 characters per token
		}
		return echoEstimatedTokens
	}

	t.Run("good input", func(t *testing.T) {
		resp, err := client.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "well hello there",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Contains(t, resp.GetOutputs()[0].GetResult(), "well hello there") //nolint:staticcheck // Intentional test use of deprecated field for backward compatibility
	})

	t.Run("good input with usage", func(t *testing.T) {
		resp, err := client.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "well hello there",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Contains(t, resp.GetOutputs()[0].GetResult(), "well hello there") //nolint:staticcheck // Intentional test use of deprecated field for backward compatibility

		// usage validation
		inputText := "well hello there"
		outputText := resp.GetOutputs()[0].GetResult()                 //nolint:staticcheck // Intentional test use of deprecated field for backward compatibility
		promptTokens := uint32(getEchoEstimatedTokens(inputText))      //nolint:gosec // Test code with safe conversion
		completionTokens := uint32(getEchoEstimatedTokens(outputText)) //nolint:gosec // Test code with safe conversion
		require.Equal(t, promptTokens+completionTokens, resp.GetUsage().GetTotalTokens())
		require.Equal(t, promptTokens, resp.GetUsage().GetPromptTokens())
		require.Equal(t, completionTokens, resp.GetUsage().GetCompletionTokens())
	})

	t.Run("good input with context", func(t *testing.T) {
		resp, err := client.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "Hello",
					Role:    ptr.Of("user"),
				},
				{
					Content: "Hi there! How can I help you?",
					Role:    ptr.Of("assistant"),
				},
				{
					Content: "What's the weather like?",
					Role:    ptr.Of("user"),
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Contains(t, resp.GetOutputs()[0].GetResult(), "What's the weather like?") //nolint:staticcheck // Intentional test use of deprecated field for backward compatibility
	})

	t.Run("bad component name", func(t *testing.T) {
		_, err := client.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "nonexistent-component",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "Hello",
				},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed finding conversation component")
	})

	t.Run("good input with usage", func(t *testing.T) {
		resp, err := client.ConverseAlpha1(ctx, &rtv1.ConversationRequest{
			Name: "echo",
			Inputs: []*rtv1.ConversationInput{
				{
					Content: "well hello there",
				},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.GetOutputs(), 1)
		require.Equal(t, "well hello there", resp.GetOutputs()[0].GetResult())

		// usage validation
		estimatedTokensInt := getEchoEstimatedTokens("well hello there")
		estimatedTokens := int32(estimatedTokensInt) //nolint:gosec // Safe conversion for test data
		require.Equal(t, 2*estimatedTokens, resp.GetUsage().GetTotalTokens())
		require.Equal(t, estimatedTokens, resp.GetUsage().GetPromptTokens())
		require.Equal(t, estimatedTokens, resp.GetUsage().GetCompletionTokens())
	})
}
