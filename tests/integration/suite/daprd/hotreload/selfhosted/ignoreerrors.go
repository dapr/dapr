/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
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
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(ignoreerrors))
}

type ignoreerrors struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
}

func (i *ignoreerrors) Setup(t *testing.T) []framework.Option {
	i.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Failed to init component a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
			"Ignoring error processing component: process component a error: [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
			"Error processing component, daprd will exit gracefully: process component a error: [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): [INIT_COMPONENT_FAILURE]: initialization error occurred for a (state.sqlite/v1): missing connection string",
		),
	)

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
`), 0o600))

	i.resDir = t.TempDir()

	i.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(i.resDir),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(i.logline),
	)

	return []framework.Option{
		framework.WithProcesses(i.logline, i.daprd),
	}
}

func (i *ignoreerrors) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)

	assert.Empty(t, i.daprd.GetMetaRegisteredComponents(t, ctx))

	t.Run("adding components should become available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(i.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: a
spec:
 type: state.in-memory
 version: v1
`), 0o600))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, i.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a component with an error should be closed and ignored if `ignoreErrors=true`", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(i.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: a
spec:
 type: state.sqlite
 version: v1
 ignoreErrors: true
`), 0o600))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Empty(t, i.daprd.GetMetaRegisteredComponents(t, ctx))
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a `ignoreErrors=true` component should make it available again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(i.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: a
spec:
 type: state.in-memory
 version: v1
`), 0o600))
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			assert.Len(t, i.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)
	})

	t.Run("Updating a component with an error should be closed and exit 1 if `ignoreErrors=false`", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(i.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: a
spec:
 type: state.sqlite
 version: v1
`), 0o600))
		i.logline.EventuallyFoundAll(t)
	})
}
