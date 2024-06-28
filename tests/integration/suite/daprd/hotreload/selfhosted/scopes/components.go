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

package scopes

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
	suite.Register(new(components))
}

type components struct {
	daprd  *daprd.Daprd
	resDir string
}

func (c *components) Setup(t *testing.T) []framework.Option {
	c.resDir = t.TempDir()

	c.daprd = daprd.New(t,
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
 name: hotreloading
spec:
 features:
 - name: HotReload
   enabled: true`),
		daprd.WithResourcesDir(c.resDir),
		daprd.WithNamespace("default"),
		daprd.WithAppID("myappid"),
	)

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *components) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	file := filepath.Join(c.resDir, "res.yaml")
	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
`), 0o600))

	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, c.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
scopes:
- foo
`), 0o600))
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Empty(t, c.daprd.GetMetaRegisteredComponents(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
scopes:
- foo
- myappid
`), 0o600))
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, c.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
scopes:
- foo
`), 0o600))
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Empty(t, c.daprd.GetMetaRegisteredComponents(t, ctx))
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
`), 0o600))
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, c.daprd.GetMetaRegisteredComponents(t, ctx), 1)
	}, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(file, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 456
spec:
 type: state.in-memory
 version: v1
scopes:
- myappid
`), 0o600))
	require.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.Len(t, c.daprd.GetMetaRegisteredComponents(t, ctx), 2)
	}, time.Second*10, time.Millisecond*10)
}
