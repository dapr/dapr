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

// locked asserts a resource file another process holds without sharing, as
// an on-access scanner does, neither stops the runtime, loses the resource
// once the hold is released, nor unloads a resource already loaded from it.
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

	component := func(name string) []byte {
		return fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: %s
spec:
  type: state.in-memory
  version: v1
  metadata: []
`, name)
	}
	registered := func(c *assert.CollectT, n int) {
		assert.Len(c, l.daprd.GetMetaRegisteredComponents(c, ctx), n)
	}

	t.Run("a hold shorter than the read retries loads the component", func(t *testing.T) {
		release := fos.WriteFileHeld(t, filepath.Join(l.resDir, "1.yaml"), component("brief"))
		time.Sleep(time.Millisecond * 50)
		release()
		require.EventuallyWithT(t, func(c *assert.CollectT) { registered(c, 1) }, time.Second*15, time.Millisecond*10)
	})

	t.Run("a hold longer than the read retries keeps the runtime up and the scan is retried", func(t *testing.T) {
		release := fos.WriteFileHeld(t, filepath.Join(l.resDir, "2.yaml"), component("long"))
		time.Sleep(time.Second)
		assert.Len(t, l.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		release()
		// Releasing a handle raises no filesystem event, so only the loader's
		// own rescan can pick the file up.
		require.EventuallyWithT(t, func(c *assert.CollectT) { registered(c, 2) }, time.Second*15, time.Millisecond*10)
	})

	t.Run("a loaded component whose file is held is not unloaded", func(t *testing.T) {
		release := fos.WriteFileHeld(t, filepath.Join(l.resDir, "1.yaml"), component("brief"))
		require.NoError(t, os.WriteFile(filepath.Join(l.resDir, "3.yaml"), component("other"), 0o600))
		assert.Never(t, func() bool {
			return len(l.daprd.GetMetaRegisteredComponents(t, ctx)) < 2
		}, time.Second, time.Millisecond*10, "a scan that cannot read a held file must not reconcile without it")
		release()
		require.EventuallyWithT(t, func(c *assert.CollectT) { registered(c, 3) }, time.Second*15, time.Millisecond*10)
	})
}
