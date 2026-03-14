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
	"github.com/dapr/dapr/tests/integration/framework/log"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resiliency))
}

// resiliency tests that resiliency resources are hot-reloaded via file
// watcher in selfhosted mode when HotReload feature is enabled.
type resiliency struct {
	daprd  *daprd.Daprd
	logOut *log.Log
	resDir string
}

func (r *resiliency) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	r.logOut = log.New()
	r.resDir = t.TempDir()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`), 0o600))

	r.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resDir),
		daprd.WithExecOptions(exec.WithStdout(r.logOut), exec.WithStderr(r.logOut)),
	)

	return []framework.Option{
		framework.WithProcesses(r.daprd),
	}
}

func (r *resiliency) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	t.Run("adding resiliency file triggers reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "resiliency.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      general: 5s
  targets: {}
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, r.logOut.Contains("myresiliency"),
				"expected log to contain resiliency name after hot reload")
		}, 20*time.Second, 10*time.Millisecond)
	})

	t.Run("updating resiliency file triggers reload", func(t *testing.T) {
		r.logOut.Reset()

		require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "resiliency.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    timeouts:
      general: 10s
  targets: {}
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, r.logOut.Contains("myresiliency"),
				"expected log to contain resiliency name after update")
		}, 20*time.Second, 10*time.Millisecond)
	})
}
