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
	"fmt"
	"io"
	"net/http"
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
	postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/test-alpha2-echo/converse", b.daprd.HTTPAddress())

	httpClient := client.HTTP(t)

	t.Run("all fields", func(t *testing.T) {
		body := `{
			"name": "test-alpha2-echo",
			"contextId": "test-conversation-123",
			"inputs": [
				{
					"messages": [
						{
							"ofUser": {
								"name": "test-user",
								"content": [
									{
										"text": "well hello there"
									}
								]
							}
						}
					],
					"scrubPII": false
				},
				{
					"messages": [
						{
							"ofSystem": {
								"name": "test-system",
								"content": [
									{
										"text": "You are a helpful assistant"
									}
								]
							}
						}
					],
					"scrubPII": true
				}
			],
			"parameters": {
				"max_tokens": {
					"@type": "type.googleapis.com/google.protobuf.Int64Value",
					"value": "100"
				},
				"model": {
					"@type": "type.googleapis.com/google.protobuf.StringValue",
					"value": "test-model"
				}
			},
			"metadata": {
				"api_key": "test-key",
				"version": "1.0"
			},
			"scrubPii": true,
			"temperature": 0.7,
			"tools": [
				{
					"function": {
						"name": "test_function",
						"description": "A test function",
						"parameters": {
							"param1": {
								"@type": "type.googleapis.com/google.protobuf.StringValue",
								"value": "string"
							}
						}
					}
				}
			],
			"toolChoice": "auto"
		}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Should have outputs for both inputs
		expectedResponse := `{
			"contextId": "test-conversation-123",
			"outputs": [
				{
					"choices": {
						"finishReason": "done",
						"message": "well hello there"
					},
					"parameters": {
						"max_tokens": {
							"@type": "type.googleapis.com/google.protobuf.Int64Value",
							"value": "100"
						},
						"model": {
							"@type": "type.googleapis.com/google.protobuf.StringValue",
							"value": "test-model"
						}
					}
				},
				{
					"choices": {
						"finishReason": "done",
						"index": "1",
						"message": "You are a helpful assistant"
					},
					"parameters": {
						"max_tokens": {
							"@type": "type.googleapis.com/google.protobuf.Int64Value",
							"value": "100"
						},
						"model": {
							"@type": "type.googleapis.com/google.protobuf.StringValue",
							"value": "test-model"
						}
					}
				}
			]
		}`
		require.JSONEq(t, expectedResponse, string(respBody))
	})

	t.Run("invalid json", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"invalidField":{"content":"invalid"}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		respBody, err := io.ReadAll(resp.Body)
		require.NotNil(t, respBody)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}
