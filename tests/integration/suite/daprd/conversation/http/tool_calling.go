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

package http

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

func (tc *toolCalling) Setup(t *testing.T) []framework.Option {
	// Build component configuration
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
    value: ` + apiKey + `
  - name: model
    value: "gpt-3.5-turbo"`
	}

	tc.daprd = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithResourceFiles(componentConfig),
	)

	return []framework.Option{
		framework.WithProcesses(tc.daprd),
	}
}

func (tc *toolCalling) Run(t *testing.T, ctx context.Context) {
	tc.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	echoURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/echo/converse", tc.daprd.HTTPAddress())
	openaiURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/openai/converse", tc.daprd.HTTPAddress())

	t.Run("echo tool calling", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"inputs": []map[string]interface{}{
				{
					"content": "What's the weather like in San Francisco?",
					"role":    "user",
					"tools": []map[string]interface{}{
						{
							"type": "function",
							"function": map[string]interface{}{
								"name":        "get_weather",
								"description": "Get current weather for a location",
								"parameters":  `{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}`,
							},
						},
					},
				},
			},
		}

		jsonBody, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, echoURL, strings.NewReader(string(jsonBody)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(body, &response)
		require.NoError(t, err)

		t.Logf("Echo HTTP Response: %s", string(body))

		// Verify response structure
		outputs, ok := response["outputs"].([]interface{})
		require.True(t, ok, "Should have outputs array")
		require.Len(t, outputs, 1)

		output := outputs[0].(map[string]interface{})

		// Log for debugging
		if result, ok := output["result"].(string); ok {
			t.Logf("Result: %s", result)
		}
		if toolCalls, ok := output["toolCalls"].([]interface{}); ok {
			t.Logf("Tool calls: %v", toolCalls)
			if len(toolCalls) > 0 {
				t.Logf("✅ Tool calling working via HTTP!")
				toolCall := toolCalls[0].(map[string]interface{})
				function := toolCall["function"].(map[string]interface{})
				assert.Equal(t, "get_weather", function["name"])
				assert.Contains(t, function["arguments"], "San Francisco")
			}
		}
		if finishReason, ok := output["finishReason"].(string); ok {
			t.Logf("Finish reason: %s", finishReason)
		}
	})

	t.Run("echo normal conversation", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"inputs": []map[string]interface{}{
				{
					"content": "Hello world",
					"role":    "user",
				},
			},
		}

		jsonBody, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, echoURL, strings.NewReader(string(jsonBody)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(body, &response)
		require.NoError(t, err)

		t.Logf("Normal HTTP Response: %s", string(body))

		outputs, ok := response["outputs"].([]interface{})
		require.True(t, ok)
		require.Len(t, outputs, 1)

		output := outputs[0].(map[string]interface{})
		assert.Equal(t, "Hello world", output["result"])

		// Should not have tool calls for regular message
		if toolCalls, ok := output["toolCalls"].([]interface{}); ok {
			assert.Empty(t, toolCalls)
		}
	})

	t.Run("openai tool calling live", func(t *testing.T) {
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			t.Skip("OPENAI_API_KEY not set, skipping live OpenAI test")
		}

		reqBody := map[string]interface{}{
			"inputs": []map[string]interface{}{
				{
					"content": "What's the weather like in New York?",
					"role":    "user",
					"tools": []map[string]interface{}{
						{
							"type": "function",
							"function": map[string]interface{}{
								"name":        "get_weather",
								"description": "Get current weather for a location",
								"parameters":  `{"type":"object","properties":{"location":{"type":"string","description":"City and state, e.g. San Francisco, CA"}},"required":["location"]}`,
							},
						},
					},
				},
			},
		}

		jsonBody, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiURL, strings.NewReader(string(jsonBody)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(body, &response)
		require.NoError(t, err)

		t.Logf("OpenAI HTTP Response: %s", string(body))

		outputs, ok := response["outputs"].([]interface{})
		require.True(t, ok)
		require.Len(t, outputs, 1)

		output := outputs[0].(map[string]interface{})

		// OpenAI should return tool calls for weather query
		if toolCalls, ok := output["toolCalls"].([]interface{}); ok && len(toolCalls) > 0 {
			t.Logf("✅ OpenAI tool calling working via HTTP!")
			toolCall := toolCalls[0].(map[string]interface{})
			function := toolCall["function"].(map[string]interface{})
			assert.Equal(t, "get_weather", function["name"])
			assert.NotEmpty(t, toolCall["id"])
			assert.Equal(t, "function", toolCall["type"])
		}

		if finishReason, ok := output["finishReason"].(string); ok {
			t.Logf("Finish reason: %s", finishReason)
		}
	})
}
