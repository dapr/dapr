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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(fileload))
}

// fileload verifies that MCPServer YAML files are loaded from disk in
// self-hosted mode. Valid resources are loaded and logged; invalid ones
// (e.g. two transports) and out-of-scope ones are skipped.
type fileload struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
}

func (s *fileload) Setup(t *testing.T) []framework.Option {
	resDir := t.TempDir()

	// ignoreErrors=true on every resource: this test runs without placement,
	// so EnsureActorsRegistered would otherwise fail and shut down daprd, and
	// the invalid-mcp validation failure would do the same. Both are expected
	// here — we're verifying file-load behaviour, not workflow invocation.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "global.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: global-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(resDir, "scoped.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: scoped-mcp
spec:
  ignoreErrors: true
  endpoint:
    sse:
      url: http://scoped.example.com/sse
scopes:
- test-app
`), 0o600))

	// Scoped to a different app — should NOT be loaded.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "other.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: other-app-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://other.example.com/mcp
scopes:
- different-app
`), 0o600))

	// Invalid: two transports — should be rejected by validation but not
	// terminate daprd because ignoreErrors=true on the spec.
	require.NoError(t, os.WriteFile(filepath.Join(resDir, "invalid.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: invalid-mcp
spec:
  ignoreErrors: true
  endpoint:
    streamableHTTP:
      url: http://example.com/mcp
    sse:
      url: http://example.com/sse
`), 0o600))

	s.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"MCPServer loaded: global-mcp",
			"MCPServer loaded: scoped-mcp",
		),
	)

	s.daprd = daprd.New(t,
		daprd.WithAppID("test-app"),
		daprd.WithResourcesDir(resDir),
		daprd.WithLogLineStdout(s.logline),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.daprd),
	}
}

func (s *fileload) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	t.Run("valid MCPServers are loaded from disk", func(t *testing.T) {
		s.logline.EventuallyFoundAll(t)
	})

	t.Run("metadata API exposes loaded MCPServers", func(t *testing.T) {
		servers := s.daprd.GetMetaMCPServers(t, ctx)

		names := make([]string, 0, len(servers))
		for _, m := range servers {
			names = append(names, m.GetName())
		}

		// Loaded: global-mcp (no scopes) and scoped-mcp (scoped to test-app).
		assert.ElementsMatch(t, []string{"global-mcp", "scoped-mcp"}, names,
			"metadata API should expose only the MCPServers loaded for this app")
		// other-app-mcp is scoped to a different app; invalid-mcp has two transports
		// and is rejected by validation. Neither should appear.
		assert.NotContains(t, names, "other-app-mcp",
			"out-of-scope MCPServer should not be exposed")
		assert.NotContains(t, names, "invalid-mcp",
			"validation-rejected MCPServer should not be exposed")
	})
}
