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
	fos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(locked))
}

// locked asserts a resource file another process holds without sharing while
// it is written, as an on-access scanner does, neither stops the runtime nor
// loses the resource once the hold is released.
type locked struct {
	daprd  *daprd.Daprd
	resDir string
}

func (l *locked) Setup(t *testing.T) []framework.Option {
	fos.SkipNotWindows(t)

	l.resDir = t.TempDir()
	l.daprd = daprd.New(t, daprd.WithResourcesDir(l.resDir))
	return []framework.Option{
		framework.WithProcesses(l.daprd),
	}
}

func (l *locked) Run(t *testing.T, ctx context.Context) {
	l.daprd.WaitUntilRunning(t, ctx)
	assert.Empty(t, l.daprd.GetMetaRegisteredComponents(t, ctx))

	t.Run("a hold shorter than the read retries loads the component", func(t *testing.T) {
		fos.WriteFileHeld(t, filepath.Join(l.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: brief
spec:
  type: state.in-memory
  version: v1
  metadata: []
`), time.Millisecond*50)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, l.daprd.GetMetaRegisteredComponents(c, ctx), 1)
		}, time.Second*15, time.Millisecond*10)
	})

	t.Run("a hold longer than the read retries keeps the runtime up", func(t *testing.T) {
		path := filepath.Join(l.resDir, "2.yaml")
		component := []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: long
spec:
  type: state.in-memory
  version: v1
  metadata: []
`)
		fos.WriteFileHeld(t, path, component, time.Second)
		// The scan which found the file locked skipped it; the runtime must
		// still answer and pick the file up on its next change.
		assert.Len(t, l.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		require.NoError(t, os.WriteFile(path, component, 0o600))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, l.daprd.GetMetaRegisteredComponents(c, ctx), 2)
		}, time.Second*15, time.Millisecond*10)
	})
}
