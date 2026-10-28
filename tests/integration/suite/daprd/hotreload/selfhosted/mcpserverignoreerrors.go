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

package selfhosted

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserverignoreerrors))
}

// mcpserverignoreerrors mirrors the components ignoreerrors suite for
// MCPServer hot-reload. It exercises:
//
//  1. Hot-reload update on an existing server with a spec that fails
//     security validation under IgnoreErrors=true: the old server is closed
//     (DeleteMCPServer side) and the new one is rejected, so compstore ends
//     up empty.
//  2. After IgnoreErrors=true failure, switching back to a valid spec
//     re-registers the server.
//  3. A hot-reload update that fails validation with IgnoreErrors=false
//     surfaces the error through the reconciler and shuts down daprd.
//
// All servers in this test use IgnoreErrors=true (except the final exit
// case) so the workflow-registration step, against a local server answering
// 404, fails without crashing daprd; we are exercising the reconciler
// error-policy machinery, not real MCP traffic.
type mcpserverignoreerrors struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
	url     string
}

func (m *mcpserverignoreerrors) Setup(t *testing.T) []framework.Option {
	m.logline = logline.New(t,
		logline.WithStdoutLineContains(
			// daprd writes the message field in logrus's quoted form, so
			// MCPServer "a" appears in the line as MCPServer \"a\".
			// Update with ignoreErrors=true and a bad scheme: logged then
			// ignored, server is not re-added.
			`Ignoring error processing MCPServer: process MCPServer a error: MCPServer \"a\" failed security validation:`,
			// Final update with ignoreErrors=false: daprd exits gracefully.
			`Error processing MCPServer, daprd will exit gracefully: process MCPServer a error: MCPServer \"a\" failed security validation:`,
		),
	)

	m.resDir = t.TempDir()
	m.url = prochttp.NotFoundURL(t) + "/mcp"

	m.daprd = daprd.New(t,
		daprd.WithResourcesDir(m.resDir),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(m.logline),
	)

	return []framework.Option{
		framework.WithProcesses(m.logline, m.daprd),
	}
}

func (m *mcpserverignoreerrors) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))

	t.Run("adding an MCPServer should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: a
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: %s
`, m.url), 0o600))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, m.daprd.GetMetaMCPServers(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating an MCPServer with an error should close existing and be ignored if `ignoreErrors=true`", func(t *testing.T) {
		// Scheme "ftp" trips MCPServerSecurity (only http/https are allowed).
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: a
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: ftp://example.com/mcp
`), 0o600))
		// Reconciler runs DeleteMCPServer (close) then AddPendingMCPServer
		// (rejected by security validation). End state: empty compstore.
		// The reconciler handles one event at a time, and this one queues
		// behind the previous step's workflow registration and its
		// DaprBuiltInInitializationRetries backoff.
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx))
		}, time.Second*15, time.Millisecond*10)
	})

	t.Run("Updating an `ignoreErrors=true` MCPServer back to valid spec should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: a
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: %s
`, m.url), 0o600))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, m.daprd.GetMetaMCPServers(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating an MCPServer with an error should be closed and exit 1 if `ignoreErrors=false`", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: a
spec:
  endpoint:
    streamableHTTP:
      url: ftp://example.com/mcp
`), 0o600))
		m.logline.EventuallyFoundAll(t)
		m.daprd.WaitUntilExit(t, 15*time.Second)
	})
}
