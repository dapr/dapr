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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fileload))
}

// fileload verifies that MCPServer YAML files are loaded from disk in
// self-hosted mode and that scoped resources are filtered by app ID.
type fileload struct {
	daprd *daprd.Daprd
}

func (s *fileload) Setup(t *testing.T) []framework.Option {
	resDir := t.TempDir()

	// MCPServer with no scopes — should be loaded.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "global.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: global-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

	// MCPServer scoped to this app — should be loaded.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "scoped.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: scoped-mcp
spec:
  endpoint:
    sse:
      url: http://scoped.example.com/sse
scopes:
- test-app
`), 0o600))

	// MCPServer scoped to a different app — should NOT be loaded.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "other.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: other-app-mcp
spec:
  endpoint:
    streamableHTTP:
      url: http://other.example.com/mcp
scopes:
- different-app
`), 0o600))

	// Mixed file: MCPServer + Component in the same YAML — only MCPServer should be picked up.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "mixed.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: mixed-mcp
spec:
  endpoint:
    stdio:
      command: echo
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.in-memory
  version: v1
`), 0o600))

	s.daprd = daprd.New(t,
		daprd.WithAppID("test-app"),
		daprd.WithResourcesDir(resDir),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: mcpconfig
spec:
  features:
  - name: MCPServerResource
    enabled: true
`),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *fileload) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("global, scoped, and mixed MCPServers are loaded; other-app is filtered", func(t *testing.T) {
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			servers := s.daprd.GetMetaMCPServers(c, ctx)
			names := mcpServerNames(servers)
			assert.Len(c, names, 3)
			assert.Contains(c, names, "global-mcp")
			assert.Contains(c, names, "scoped-mcp")
			assert.Contains(c, names, "mixed-mcp")
			assert.NotContains(c, names, "other-app-mcp")
		}, 10*time.Second, 100*time.Millisecond)
	})
}

func mcpServerNames(servers []*rtv1.MetadataMCPServer) []string {
	names := make([]string, len(servers))
	for i, s := range servers {
		names[i] = s.GetName()
	}
	return names
}
