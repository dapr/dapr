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

package ports

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/inmemory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalgrpc))
}

// internalgrpc verifies that daprd reserves (binds) its internal gRPC port
// before it initializes components. Otherwise a component's outbound
// connection can be assigned this port as an ephemeral source port.
type internalgrpc struct {
	daprd       *procdaprd.Daprd
	initEntered chan struct{}
	release     chan struct{}
}

func (i *internalgrpc) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	i.initEntered = make(chan struct{})
	i.release = make(chan struct{})

	sock := socket.New(t)
	store := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(&blockingStore{
			Store:       inmemory.New(t),
			initEntered: i.initEntered,
			release:     i.release,
		}),
	)

	i.daprd = procdaprd.New(t,
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
`, store.SocketName())),
		procdaprd.WithSocket(t, sock),
	)

	return []framework.Option{
		framework.WithProcesses(store, i.daprd),
	}
}

func (i *internalgrpc) Run(t *testing.T, ctx context.Context) {
	// Wait until daprd is blocked inside the component's Init: component
	// initialization has started but not yet completed.
	select {
	case <-i.initEntered:
	case <-time.After(time.Second * 30):
		t.Fatal("timed out waiting for component Init to be entered")
	}

	// The internal gRPC port must already be listening even though component
	// initialization has not finished.
	addr := fmt.Sprintf("localhost:%d", i.daprd.InternalGRPCPort())
	dialer := net.Dialer{Timeout: time.Second}
	assert.Eventuallyf(t, func() bool {
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return false
		}
		require.NoError(t, conn.Close())
		return true
	}, time.Second*10, 10*time.Millisecond, "internal gRPC port (%s) was not reserved before component init", addr)

	// Unblock initialization and confirm daprd finishes starting up.
	close(i.release)
	i.daprd.WaitUntilRunning(t, ctx)
}

// blockingStore wraps a state store and blocks in Init until released,
// signalling initEntered the first time Init is called.
type blockingStore struct {
	state.Store
	initEntered chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (b *blockingStore) Init(ctx context.Context, meta state.Metadata) error {
	// The test framework calls Init once during its own setup with empty
	// metadata (see statestore.go). Only gate the Init initiated by daprd over
	// the pluggable gRPC channel, which always carries a non-empty component
	// name (see the pluggable statestore component wrapper).
	if meta.Name == "" {
		return b.Store.Init(ctx, meta)
	}
	b.once.Do(func() { close(b.initEntered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.Store.Init(ctx, meta)
}
