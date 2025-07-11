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
	"os"
	"testing"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type liveConversationAIProvider struct {
	componentName         string
	envVar                string
	model                 string
	description           string
	toolStreamingDisabled bool
}

// realConversationAIProviders is a list of real AI providers that are supported by the conversation API
// We run providers tests conditionally based on the API key being set in the environment
// you can run the tests with the API keys set in the environment by running:
//
//	OPENAI_API_KEY=your-key ANTHROPIC_API_KEY=your-key GOOGLE_AI_API_KEY=your-key CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/multiTurnMultiTool"
//
// or you can source the env vars from the .env file by running (for example if located in .local/.env):
//
//	source .local/.env && OPENAI_API_KEY=$OPENAI_API_KEY ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY GOOGLE_AI_API_KEY=$GOOGLE_AI_API_KEY CGO_ENABLED=1 go test ./tests/integration -timeout=20m -count=1 -v -tags="integration" -integration-parallel=false -focus="daprd/conversation/grpc/multiTurnMultiTool"
var realConversationAIProviders = []liveConversationAIProvider{
	{
		componentName: "openai",
		envVar:        "OPENAI_API_KEY",
		model:         "gpt-4.1-2025-04-14",       // "o4-mini-2025-04-16",
		description:   "gpt-4.1-2025-04-14 model", // "OpenAI's o4-mini model",
	},
	{
		componentName:         "anthropic",
		envVar:                "ANTHROPIC_API_KEY",
		model:                 "claude-sonnet-4-20250514",
		description:           "Anthropic's claude-sonnet-4 model",
		toolStreamingDisabled: false, // try to use simulated streaming
	},
	{
		componentName: "googleai",
		envVar:        "GOOGLE_AI_API_KEY",
		model:         "gemini-2.5-flash",
		description:   "Google's gemini-2.5-flash model",
	},
}

// getEchoComponentConfig returns the component config for the echo component
func getEchoComponentConfig() string {
	return `
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
`
}

// buildLiveAIProviderComponents builds the component config for the live AI providers and includes the echo component config too
func buildLiveAIProviderComponents(t *testing.T) string {
	t.Helper()
	componentConfig := getEchoComponentConfig()

	for _, provider := range realConversationAIProviders {
		if apiKey := os.Getenv(provider.envVar); apiKey != "" {
			t.Logf("Found API key for %s, adding component config", provider.componentName)
			componentConfig += `
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: ` + provider.componentName + `
spec:
  type: conversation.` + provider.componentName + `
  version: v1
  metadata:
  - name: key
    value: ` + apiKey + `
  - name: model
    value: ` + provider.model
		}
	}

	return componentConfig
}

// Helper function to extract tool calls from content parts for debugging
func extractToolCallsFromParts(parts []*runtimev1pb.ConversationContent) []*runtimev1pb.ConversationToolCall {
	var toolCalls []*runtimev1pb.ConversationToolCall
	for _, part := range parts {
		if toolCall := part.GetToolCall(); toolCall != nil {
			toolCalls = append(toolCalls, toolCall)
		}
	}
	return toolCalls
}
