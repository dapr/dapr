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

package actorstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contribstate "github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	frameworkos "github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(initretry))
}

// initretry hot-reloads a healthy actor state store to a broken
// spec (init retries in place), then to a corrected spec. The fix must
// supersede the stale retry loop, which requires the component reconciler
// to not block on the broken init's result. Boot-time failures are out of
// scope: the hot reloader only starts after initial component processing.
type initretry struct {
	daprd             *daprd.Daprd
	store             *gatedInitStore
	loglineRetry      *logline.LogLine
	loglineSuperseded *logline.LogLine
	resDir            string
	componentManifest string
}

// gatedInitStore fails Init only for generation=2, decoupling the broken
// spec from retry timing.
type gatedInitStore struct {
	*inmemory.WrappedTransactionalMultiMaxSize

	attempts atomic.Int32
}

func (s *gatedInitStore) Init(ctx context.Context, md contribstate.Metadata) error {
	s.attempts.Add(1)
	if md.Properties["generation"] == "2" {
		return errors.New("connection refused: state store down")
	}
	return s.WrappedTransactionalMultiMaxSize.Init(ctx, md)
}

func (a *initretry) Setup(t *testing.T) []framework.Option {
	frameworkos.SkipWindows(t)

	a.store = &gatedInitStore{
		WrappedTransactionalMultiMaxSize: inmemory.NewTransactionalMultiMaxSize(t).(*inmemory.WrappedTransactionalMultiMaxSize),
	}

	sock := socket.New(t)
	ss := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(a.store),
	)

	a.componentManifest = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
  - name: generation
    value: "%%s"
`, ss.SocketName())

	a.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"),
		fmt.Appendf(nil, a.componentManifest, "1"), 0o600))

	a.loglineRetry = logline.New(t, logline.WithStdoutLineContains(
		"Retrying init of actor state store mystore",
	))
	a.loglineSuperseded = logline.New(t, logline.WithStdoutLineContains(
		"Stopping init retry of mystore",
	))

	a.daprd = daprd.New(t,
		daprd.WithSocket(t, sock),
		daprd.WithResourcesDir(a.resDir),
		daprd.WithExecOptions(
			exec.WithStdout(iowriter.NewMultiWriteCloser(
				iowriter.New(t, "daprd"),
				a.loglineRetry.Stdout(),
				a.loglineSuperseded.Stdout(),
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(ss, a.loglineRetry, a.loglineSuperseded, a.daprd),
	}
}

func (a *initretry) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)
	require.GreaterOrEqual(t, a.store.attempts.Load(), int32(1))

	// Hot reload to a broken spec: init retries in place.
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"),
		fmt.Appendf(nil, a.componentManifest, "2"), 0o600))
	a.loglineRetry.EventuallyFoundAll(t)

	// The corrected spec must supersede the retry and initialize.
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"),
		fmt.Appendf(nil, a.componentManifest, "3"), 0o600))

	a.loglineSuperseded.EventuallyFoundAll(t)

	a.daprd.WaitUntilRunning(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		comps := a.daprd.GetMetaRegisteredComponents(c, ctx)
		found := false
		for _, comp := range comps {
			if comp.GetName() == "mystore" {
				found = true
			}
		}
		assert.True(c, found, "the corrected actor state store must be committed")
	}, time.Second*10, time.Millisecond*10)
}
