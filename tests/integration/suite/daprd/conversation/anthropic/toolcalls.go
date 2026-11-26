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

package anthropic

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
	procanthropic "github.com/dapr/dapr/tests/integration/framework/process/http/anthropic"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(toolcalls))
}

// toolcalls tests that the Anthropic conversation component accepts a history
// whose assistant turn only calls tools. Clients such as dapr-agents send that
// turn with an empty text content, which Anthropic rejects as a text block.
type toolcalls struct {
	anthropic *procanthropic.Anthropic
	daprd     *daprd.Daprd
}

func (c *toolcalls) Setup(t *testing.T) []framework.Option {
	c.anthropic = procanthropic.New(t, procanthropic.WithAPIKey("test-key"))

	c.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: claude
spec:
  type: conversation.anthropic
  version: v1
  metadata:
  - name: endpoint
    value: %s
  - name: key
    value: test-key
`, c.anthropic.Endpoint())),
	)

	return []framework.Option{
		framework.WithProcesses(c.anthropic, c.daprd),
	}
}

func (c *toolcalls) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	postURL := fmt.Sprintf("http://%s/v1.0-alpha2/conversation/claude/converse", c.daprd.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(`{
"inputs":[{"messages":[
  {"ofUser":{"content":[{"text":"read a"}]}},
  {"ofAssistant":{"content":[{"text":""}],"toolCalls":[{"id":"t1","function":{"name":"read","arguments":"{\"path\":\"a\"}"}}]}},
  {"ofTool":{"toolId":"t1","name":"read","content":[{"text":"A"}]}}
]}],
"tools":[{"function":{"name":"read","description":"read a file","parameters":{"type":"object"}}}]
}`))
	require.NoError(t, err)

	resp, err := client.HTTP(t).Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "hello from claude")
	assert.Equal(t, 1, c.anthropic.Messages())
}
