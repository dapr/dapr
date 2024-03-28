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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(scopes))
}

type scopes struct {
	daprd *daprd.Daprd

	resDir string
}

func (s *scopes) Setup(t *testing.T) []framework.Option {
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

	s.resDir = t.TempDir()

	s.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(s.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(s.daprd),
	}
}

func (s *scopes) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	assert.Empty(t, util.GetMetaComponents(t, ctx, client, s.daprd.HTTPPort()))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.in-memory
 version: v1
`), 0o600))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.in-memory
 version: v1
scopes:
- not-my-app-id
`), 0o600))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()))
	}, time.Second*5, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(
		fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.in-memory
 version: v1
scopes:
- not-my-app-id
- '%s'
`, s.daprd.AppID())), 0o600))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.in-memory
 version: v1
scopes:
- not-my-app-id
`), 0o600))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Empty(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()))
	}, time.Second*5, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: '123'
spec:
 type: state.in-memory
 version: v1
`), 0o600))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(c, ctx, client, s.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*10)
}
