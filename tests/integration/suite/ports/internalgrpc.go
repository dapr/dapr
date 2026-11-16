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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
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
	initEntered <-chan struct{}
	release     func()
}

func (i *internalgrpc) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	held := fault.New(t)
	i.initEntered, i.release = held.ArmInitHold()

	sock := socket.New(t)
	store := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(held),
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
	i.release()
	i.daprd.WaitUntilRunning(t, ctx)
}
