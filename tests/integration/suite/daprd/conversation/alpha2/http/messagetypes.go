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
	suite.Register(new(messagetypes))
}

type messagetypes struct {
	daprd *daprd.Daprd
}

func (m *messagetypes) Setup(t *testing.T) []framework.Option {
	m.daprd = daprd.New(t, daprd.WithResourceFiles(`
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
		framework.WithProcesses(m.daprd),
	}
}

func (m *messagetypes) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)
	postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/test-alpha2-echo/converse", m.daprd.HTTPAddress())

	httpClient := client.HTTP(t)

	// Test all message types
	t.Run("of_user", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"ofUser":{"name":"user name","content":[{"text":"user message"}]}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `{"outputs":[{"choices":{"finishReason":"done","message":"user message"}}]}`, string(respBody))
	})

	t.Run("of_system", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"ofSystem":{"name":"system name","content":[{"text":"system message"}]}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `{"outputs":[{"choices":{"finishReason":"done","message":"system message"}}]}`, string(respBody))
	})

	t.Run("of_developer", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"ofDeveloper":{"name":"dev name","content":[{"text":"developer message"}]}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `{"outputs":[{"choices":{"finishReason":"done","message":"developer message"}}]}`, string(respBody))
	})

	t.Run("of_assistant", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"ofAssistant":{"name":"assistant name","content":[{"text":"assistant message"}]}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `{"outputs":[{"choices":{"finishReason":"done","message":"assistant message"}}]}`, string(respBody))
	})

	t.Run("of_tool", func(t *testing.T) {
		body := `{"inputs":[{"messages":[{"ofTool":{"toolId":"tool-123","name":"tool name","content":[{"text":"tool message"}]}}]}]}`

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(body))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.JSONEq(t, `{"outputs":[{"choices":{"finishReason":"done","message":"tool message"}}]}`, string(respBody))
	})
}
