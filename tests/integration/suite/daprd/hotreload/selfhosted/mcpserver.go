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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(mcpserver))
}

// mcpserver verifies that MCPServer resources are detected by the hot-reload filewatcher.
// The metadata API does not yet expose MCPServers, so we verify via log matching.
// TODO: revise this test to check the metadata API once MCPServers are exposed.
type mcpserver struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
}

func (m *mcpserver) Setup(t *testing.T) []framework.Option {
	m.resDir = t.TempDir()
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpserver
spec:
  features:
  - name: MCPServerResource
    enabled: true
`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "mcp.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

	m.logline = logline.New(t,
		logline.WithStdoutLineContains("MCPServer loaded: weather-mcp"),
	)

	m.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(m.resDir),
		daprd.WithLogLineStdout(m.logline),
		daprd.WithExecOptions(exec.WithEnvVars(t)),
	)

	return []framework.Option{
		framework.WithProcesses(m.logline, m.daprd),
	}
}

func (m *mcpserver) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial MCPServer is loaded from disk", func(t *testing.T) {
		m.logline.EventuallyFoundAll(t)
	})

	// TODO(sicoyle): Once the metadata API exposes MCPServers,
	// replace log assertions with metadata API checks and add full hot-reload lifecycle tests:
	// - Add second MCPServer → metadata shows 2
	// - Delete MCPServer → metadata shows 1
	// - Re-add MCPServer → metadata shows 2
	t.Run("metadata API does not yet expose MCPServers on this branch", func(t *testing.T) {
		assert.Empty(t, m.daprd.GetMetaMCPServers(t, ctx),
			"MCPServers are not yet exposed via metadata API; activation logic is in feat-mcp-crd-plus-rest")
	})
}
