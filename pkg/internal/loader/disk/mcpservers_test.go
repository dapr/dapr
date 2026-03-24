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

package disk

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMCPServers(t *testing.T) {
	t.Run("valid MCPServer manifest is loaded", func(t *testing.T) {
		tmp := t.TempDir()
		yaml := `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: github
spec:
  endpoint:
    transport: streamable_http
    target:
      url: https://api.githubcopilot.com/mcp/
  headers:
  - name: Authorization
    value: Bearer mytoken
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "github.yaml"), []byte(yaml), fs.FileMode(0o600)))

		loader := NewMCPServers(Options{Paths: []string{tmp}})
		servers, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, servers, 1)
		assert.Equal(t, "github", servers[0].Name)
		assert.Equal(t, "streamable_http", string(servers[0].Spec.Endpoint.Transport))
		assert.Equal(t, "https://api.githubcopilot.com/mcp/", servers[0].Spec.Endpoint.Target.URL)
		require.Len(t, servers[0].Spec.Headers, 1)
		assert.Equal(t, "Authorization", servers[0].Spec.Headers[0].Name)
	})

	t.Run("non-MCPServer manifests in same file are skipped", func(t *testing.T) {
		tmp := t.TempDir()
		yaml := `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp1
spec:
  endpoint:
    transport: sse
    target:
      url: https://mcp.example.com/
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: statestore
spec:
  type: state.redis
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "mixed.yaml"), []byte(yaml), fs.FileMode(0o600)))

		loader := NewMCPServers(Options{Paths: []string{tmp}})
		servers, err := loader.Load(t.Context())
		require.NoError(t, err)
		require.Len(t, servers, 1)
		assert.Equal(t, "mcp1", servers[0].Name)
	})

	t.Run("empty directory returns no servers", func(t *testing.T) {
		tmp := t.TempDir()
		loader := NewMCPServers(Options{Paths: []string{tmp}})
		servers, err := loader.Load(t.Context())
		require.NoError(t, err)
		assert.Empty(t, servers)
	})

	t.Run("scoped MCPServer is included only for matching app ID", func(t *testing.T) {
		tmp := t.TempDir()
		yaml := `
apiVersion: dapr.io/v1alpha1
kind: MCPServer
metadata:
  name: scoped-mcp
spec:
  endpoint:
    transport: streamable_http
    target:
      url: https://mcp.example.com/
scopes:
- myapp
`
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "scoped.yaml"), []byte(yaml), fs.FileMode(0o600)))

		loaderMatch := NewMCPServers(Options{Paths: []string{tmp}, AppID: "myapp"})
		servers, err := loaderMatch.Load(t.Context())
		require.NoError(t, err)
		assert.Len(t, servers, 1)

		loaderNoMatch := NewMCPServers(Options{Paths: []string{tmp}, AppID: "otherapp"})
		servers, err = loaderNoMatch.Load(t.Context())
		require.NoError(t, err)
		assert.Empty(t, servers)
	})
}
