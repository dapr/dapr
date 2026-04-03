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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(hotreload))
}

// hotreload verifies that MCPServer resources are hot-reloaded when YAML files
// are created, updated, or deleted in the resources directory at runtime.
type hotreload struct {
	daprd  *daprd.Daprd
	resDir string
}

func (s *hotreload) Setup(t *testing.T) []framework.Option {
	s.resDir = t.TempDir()
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true
  - name: MCPServerResource
    enabled: true
`), 0o600))

	// Start with one MCPServer on disk.
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "mcp.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

	s.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(s.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *hotreload) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial MCPServer is loaded at startup", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 1)
			if len(servers) > 0 {
				assert.Equal(c, "weather-mcp", servers[0].GetName())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("adding a second MCPServer is detected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "second.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: tools-mcp
spec:
  endpoint:
    stdio:
      command: echo
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 2)
			names := make([]string, len(servers))
			for i, srv := range servers {
				names[i] = srv.GetName()
			}
			assert.Contains(c, names, "weather-mcp")
			assert.Contains(c, names, "tools-mcp")
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("deleting an MCPServer removes it", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(s.resDir, "mcp.yaml")))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 1)
			if len(servers) > 0 {
				assert.Equal(c, "tools-mcp", servers[0].GetName())
			}
		}, 10*time.Second, 100*time.Millisecond)
	})

	t.Run("re-adding the deleted MCPServer is detected", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "mcp.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: weather-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd.GetMetaMCPServers(c, ctx)
			assert.Len(c, servers, 2)
			names := make([]string, len(servers))
			for i, srv := range servers {
				names[i] = srv.GetName()
			}
			assert.Contains(c, names, "weather-mcp")
			assert.Contains(c, names, "tools-mcp")
		}, 10*time.Second, 100*time.Millisecond)
	})
}
