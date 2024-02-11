/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwn.apache.org/licenses/LICENSE-2.0
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

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(namespace))
}

// namespace ensures that component manifest files are hot-reloaded, regardless
// of the namespace set on them.
type namespace struct {
	daprd   *daprd.Daprd
	logline *logline.LogLine
	resDir  string
}

func (n *namespace) Setup(t *testing.T) []framework.Option {
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

	n.resDir = t.TempDir()

	n.logline = logline.New(t, logline.WithStdoutLineContains(
		"Fatal error from runtime: failed to load resources from disk: duplicate definition of component name foo (pubsub.in-memory/v1) with existing foo (state.in-memory/v1)",
	))

	n.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(n.resDir),
		daprd.WithNamespace("mynamespace"),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(n.logline),
	)

	require.NoError(t, os.WriteFile(filepath.Join(n.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
`), 0o600))

	return []framework.Option{
		framework.WithProcesses(n.logline, n.daprd),
	}
}

func (n *namespace) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, n.daprd.RegistedComponents(t, ctx))
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(n.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: default
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "pubsub.in-memory", Version: "v1",
				Capabilities: []string{"SUBSCRIBE_WILDCARDS"},
			},
		}, n.daprd.RegistedComponents(t, ctx))
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(n.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
`), 0o600))

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, n.daprd.RegistedComponents(t, ctx))
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(n.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
 namespace: foobar
spec:
 type: state.in-memory
 version: v1
`), 0o600))

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		assert.ElementsMatch(t, []*rtv1.RegisteredComponents{
			{
				Name: "123", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
			{
				Name: "foo", Type: "state.in-memory", Version: "v1",
				Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
			},
		}, n.daprd.RegistedComponents(t, ctx))
	}, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(n.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
 namespace: foobar
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
 namespace: mynamespace
spec:
 type: pubsub.in-memory
 version: v1
`), 0o600))

	n.logline.EventuallyFoundAll(t)
}
