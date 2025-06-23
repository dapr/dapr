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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(toolCalling))
}

type toolCalling struct {
	daprd *daprd.Daprd
}

type ConversationRequest struct {
	Inputs []ConversationInput `json:"inputs"`
}

type ConversationInput struct {
	Content string `json:"content"`
	Role    string `json:"role,omitempty"`
	Tools   []Tool `json:"tools,omitempty"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"`
}

type ConversationResponse struct {
	Outputs []ConversationResult `json:"outputs"`
}

type ConversationResult struct {
	Result       string     `json:"result"`
	ToolCalls    []ToolCall `json:"toolCalls,omitempty"`
	FinishReason string     `json:"finishReason,omitempty"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (tc *toolCalling) Setup(t *testing.T) []framework.Option {
	// Build component configuration - always include echo, conditionally include OpenAI if API key available
	componentConfig := `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: echo
spec:
  type: conversation.echo
  version: v1
  metadata:
  - name: model
    value: "echo-model"`

	// Add OpenAI component if API key is available
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		componentConfig += `
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: openai
spec:
  type: conversation.openai
  version: v1
  metadata:
  - name: key
    value: ` + apiKey
	}

	tc.daprd = daprd.New(t, daprd.WithResourceFiles(componentConfig))

	return []framework.Option{
		framework.WithProcesses(tc.daprd),
	}
}

func (tc *toolCalling) Run(t *testing.T, ctx context.Context) {
	tc.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	t.Run("echo tool calling via HTTP", func(t *testing.T) {
		postURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/echo/converse", tc.daprd.HTTPAddress())

		// Test with tool calling request
		reqBody := ConversationRequest{
			Inputs: []ConversationInput{
				{
					Content: "What's the weather like in San Francisco?",
					Role:    "user",
					Tools: []Tool{
						{
							Type: "function",
							Function: ToolFunction{
								Name:        "get_weather",
								Description: "Get current weather for a location",
								Parameters:  `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`,
							},
						},
					},
				},
			},
		}

		reqJSON, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(string(reqJSON)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		t.Logf("HTTP Tool Calling Response: %s", string(respBody))

		var convResp ConversationResponse
		err = json.Unmarshal(respBody, &convResp)
		require.NoError(t, err)

		require.Len(t, convResp.Outputs, 1)
		output := convResp.Outputs[0]

		// Echo should recognize the weather keywords and suggest tool calling
		assert.Contains(t, output.Result, "tools")
		assert.NotEmpty(t, output.ToolCalls, "Echo should generate tool calls for weather queries")
		assert.Equal(t, "tool_calls", output.FinishReason)

		// Verify tool call structure
		toolCall := output.ToolCalls[0]
		assert.Equal(t, "function", toolCall.Type)
		assert.Equal(t, "get_weather", toolCall.Function.Name)
		assert.NotEmpty(t, toolCall.ID)

		t.Logf("✅ HTTP Tool calling working with echo!")
	})

	t.Run("echo normal conversation via HTTP", func(t *testing.T) {
		postURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/echo/converse", tc.daprd.HTTPAddress())

		// Test normal conversation without tools
		reqBody := ConversationRequest{
			Inputs: []ConversationInput{
				{
					Content: "Hello world",
					Role:    "user",
				},
			},
		}

		reqJSON, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(string(reqJSON)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		var convResp ConversationResponse
		err = json.Unmarshal(respBody, &convResp)
		require.NoError(t, err)

		require.Len(t, convResp.Outputs, 1)
		output := convResp.Outputs[0]

		assert.Equal(t, "Hello world", output.Result)
		assert.Empty(t, output.ToolCalls)

		t.Logf("✅ HTTP Normal conversation working!")
	})

	// Test with real OpenAI if API key is available
	if os.Getenv("OPENAI_API_KEY") != "" {
		t.Run("openai tool calling via HTTP", func(t *testing.T) {
			postURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/openai/converse", tc.daprd.HTTPAddress())

			reqBody := ConversationRequest{
				Inputs: []ConversationInput{
					{
						Content: "What's the weather like in New York? Please use the get_weather function.",
						Role:    "user",
						Tools: []Tool{
							{
								Type: "function",
								Function: ToolFunction{
									Name:        "get_weather",
									Description: "Get current weather for a location",
									Parameters:  `{"type":"object","properties":{"location":{"type":"string","description":"City and state"}},"required":["location"]}`,
								},
							},
						},
					},
				},
			}

			reqJSON, err := json.Marshal(reqBody)
			require.NoError(t, err)

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(string(reqJSON)))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())

			t.Logf("OpenAI HTTP Tool Calling Response: %s", string(respBody))

			var convResp ConversationResponse
			err = json.Unmarshal(respBody, &convResp)
			require.NoError(t, err)

			require.Len(t, convResp.Outputs, 1)
			output := convResp.Outputs[0]

			if len(output.ToolCalls) > 0 {
				t.Logf("✅ HTTP Tool calling working with OpenAI!")
				toolCall := output.ToolCalls[0]
				assert.Equal(t, "function", toolCall.Type)
				assert.Equal(t, "get_weather", toolCall.Function.Name)
				assert.NotEmpty(t, toolCall.ID)
				assert.Equal(t, "tool_calls", output.FinishReason)
			} else {
				t.Logf("ℹ️ OpenAI didn't return tool calls via HTTP - this might be expected")
			}
		})
	}
}
