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

package http

import (
	"context"
	"encoding/json"
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
	suite.Register(new(scrubPII))
}

type scrubPII struct {
	daprd *daprd.Daprd
}

func (s *scrubPII) Setup(t *testing.T) []framework.Option {
	s.daprd = daprd.New(t, daprd.WithResourceFiles(`
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
`))

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *scrubPII) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)
	postURL := fmt.Sprintf("http://%s/v1.0-alpha1/conversation/echo/converse", s.daprd.HTTPAddress())

	httpClient := client.HTTP(t)

	t.Run("scrub input phone number", func(t *testing.T) {
		body := `{"inputs":[{"content":"well hello there, my phone number is +2222222222", "scrubPII": true}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Parse the JSON response and check that it contains the scrubbed content
		var conversationResp ConversationResponse
		require.NoError(t, json.Unmarshal(respBody, &conversationResp))
		require.Len(t, conversationResp.Outputs, 1)
		require.Contains(t, conversationResp.Outputs[0].Result, "well hello there, my phone number is <PHONE_NUMBER>")
	})

	t.Run("scrub input email", func(t *testing.T) {
		body := `{"inputs":[{"content":"well hello there, my email is test@test.com", "scrubPII": true}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Parse the JSON response and check that it contains the scrubbed content
		var conversationResp ConversationResponse
		require.NoError(t, json.Unmarshal(respBody, &conversationResp))
		require.Len(t, conversationResp.Outputs, 1)
		require.Contains(t, conversationResp.Outputs[0].Result, "well hello there, my email is <EMAIL_ADDRESS>")
	})

	t.Run("scrub input ip address", func(t *testing.T) {
		body := `{"inputs":[{"content":"well hello there from 10.8.9.1", "scrubPII": true}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Parse the JSON response and check that it contains the scrubbed content
		var conversationResp ConversationResponse
		require.NoError(t, json.Unmarshal(respBody, &conversationResp))
		require.Len(t, conversationResp.Outputs, 1)
		require.Contains(t, conversationResp.Outputs[0].Result, "well hello there from <IP>")
	})

	t.Run("scrub all outputs for PII", func(t *testing.T) {
		// Test PII scrubbing with concatenated input (since echo only returns last message)
		combinedInput := "well hello there from 10.8.9.1 well hello there, my email is test@test.com"
		body := fmt.Sprintf(`{"inputs":[{"content":"%s"}],"scrubPII": true}`, combinedInput)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Parse the JSON response and check that it contains the scrubbed content
		var conversationResp ConversationResponse
		require.NoError(t, json.Unmarshal(respBody, &conversationResp))
		require.Len(t, conversationResp.Outputs, 1)
		// Check that both IP and email are scrubbed in the result
		require.Contains(t, conversationResp.Outputs[0].Result, "<IP>")
		require.Contains(t, conversationResp.Outputs[0].Result, "<EMAIL_ADDRESS>")
	})

	t.Run("no scrubbing on good input", func(t *testing.T) {
		body := `{"inputs":[{"content":"well hello there","scrubPII": true}],"scrubPII": true}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// Parse the JSON response and check that it contains the expected content
		var conversationResp ConversationResponse
		require.NoError(t, json.Unmarshal(respBody, &conversationResp))
		require.Len(t, conversationResp.Outputs, 1)
		require.Contains(t, conversationResp.Outputs[0].Result, "well hello there")
	})
}
