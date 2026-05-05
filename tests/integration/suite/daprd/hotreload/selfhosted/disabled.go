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

	rtpbv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled verifies that when the HotReload feature is explicitly disabled
// via Configuration, components present at startup are still loaded once,
// but subsequent filesystem changes are NOT picked up.
type disabled struct {
	daprd  *daprd.Daprd
	resDir string
}

func (d *disabled) Setup(t *testing.T) []framework.Option {
	d.resDir = t.TempDir()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloaddisabled
spec:
  features:
  - name: HotReload
    enabled: false
`), 0o600))

	require.NoError(t, os.WriteFile(filepath.Join(d.resDir, "initial.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'initial'
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), 0o600))

	d.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(d.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(d.daprd),
	}
}

func (d *disabled) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	t.Run("initial component from disk is loaded at startup", func(t *testing.T) {
		resp := d.daprd.GetMetaRegisteredComponents(t, ctx)
		assert.ElementsMatch(t, []*rtpbv1.RegisteredComponents{
			{
				Name: "initial", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "KEYS_LIKE", "ACTOR"},
			},
		}, resp)
	})

	t.Run("adding a component file after startup is not picked up", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(d.resDir, "added.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'added'
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), 0o600))

		assert.Never(t, func() bool {
			return len(d.daprd.GetMetaRegisteredComponents(t, ctx)) != 1
		}, time.Second*3, time.Millisecond*100,
			"hot reload is disabled so added component must not be loaded")
	})

	t.Run("deleting a component file after startup does not remove it", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(d.resDir, "initial.yaml")))

		assert.Never(t, func() bool {
			return len(d.daprd.GetMetaRegisteredComponents(t, ctx)) != 1
		}, time.Second*3, time.Millisecond*100,
			"hot reload is disabled so deleted component must stay registered")
	})
}
