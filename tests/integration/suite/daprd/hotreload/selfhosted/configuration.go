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
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(configuration))
}

// configuration tests that Configuration resources are hot-reloaded via file
// watcher in selfhosted mode when HotReload feature is enabled.
type configuration struct {
	daprd  *daprd.Daprd
	logOut *log.Log
	resDir string
}

func (c *configuration) Setup(t *testing.T) []framework.Option {
	c.logOut = log.New()
	c.resDir = t.TempDir()

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "0"`), 0o600))

	testApp := app.New(t,
		app.WithHandlerFunc("/hi", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "OK")
		}),
	)

	c.daprd = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(c.resDir),
		daprd.WithExecOptions(exec.WithStdout(c.logOut), exec.WithStderr(c.logOut)),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, c.daprd),
	}
}

func (c *configuration) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	t.Run("adding configuration resource file triggers reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(c.resDir, "config-resource.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myconfig
spec:
  tracing:
    samplingRate: "1"
`), 0o600))

		assert.EventuallyWithT(t, func(ct *assert.CollectT) {
			assert.True(ct, c.logOut.Contains("myconfig"),
				"expected log to contain configuration resource name after hot reload")
		}, 20*time.Second, 10*time.Millisecond)
	})
}
