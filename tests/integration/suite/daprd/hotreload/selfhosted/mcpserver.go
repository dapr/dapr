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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserver))
}

// mcpserver verifies MCPServer hot-reload lifecycle via the metadata API:
// initial load, add, delete, re-add.
type mcpserver struct {
	daprd  *daprd.Daprd
	resDir string
}

func (m *mcpserver) Setup(t *testing.T) []framework.Option {
	m.resDir = t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "mcp.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

	m.daprd = daprd.New(t,
		daprd.WithResourcesDir(m.resDir),
		daprd.WithExecOptions(exec.WithEnvVars(t)),
	)

	return []framework.Option{
		framework.WithProcesses(m.daprd),
	}
}

func (m *mcpserver) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial MCPServer appears in metadata", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 1)
			if len(servers) > 0 {
				assert.Equal(c, "weather-mcp", servers[0].GetName())
			}
		}, 10*time.Second, 200*time.Millisecond)
	})

	t.Run("add second MCPServer via hot-reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "mcp2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: tools-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://example.com/tools
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 2)
		}, 10*time.Second, 200*time.Millisecond)
	})

	t.Run("delete MCPServer via hot-reload", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(m.resDir, "mcp2.yaml")))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 1)
			if len(servers) > 0 {
				assert.Equal(c, "weather-mcp", servers[0].GetName())
			}
		}, 10*time.Second, 200*time.Millisecond)
	})

	t.Run("re-add MCPServer via hot-reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "mcp3.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: re-added-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://example.com/readded
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := m.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 2)
		}, 10*time.Second, 200*time.Millisecond)
	})
}
