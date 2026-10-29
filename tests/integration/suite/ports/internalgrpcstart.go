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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(internalgrpcstart))
}

// internalgrpcstart verifies that daprd holds its internal gRPC port before
// its first outbound connection. An outbound connection given that port as
// its source port would otherwise block the bind for as long as it lives.
// Sentry is a bare listener that never answers, so daprd stays in its
// first outbound call, fetching its identity.
type internalgrpcstart struct {
	daprd  *procdaprd.Daprd
	sentry net.Listener
	conn   net.Conn
}

func (i *internalgrpcstart) Setup(t *testing.T) []framework.Option {
	// Not run: only its trust anchors are used.
	sen := procsentry.New(t)

	var err error
	i.sentry, err = net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	// Runs after daprd has stopped, so daprd is still waiting on sentry when
	// it is stopped and exits the same way every time.
	t.Cleanup(func() {
		if i.conn != nil {
			i.conn.Close()
		}
		i.sentry.Close()
	})

	i.daprd = procdaprd.New(t,
		procdaprd.WithSentryAddress(i.sentry.Addr().String()),
		procdaprd.WithEnableMTLS(true),
		procdaprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sen.CABundle().X509.TrustAnchors)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(i.daprd),
	}
}

func (i *internalgrpcstart) Run(t *testing.T, ctx context.Context) {
	require.NoError(t, i.sentry.(*net.TCPListener).SetDeadline(time.Now().Add(20*time.Second)))
	var err error
	i.conn, err = i.sentry.Accept()
	require.NoError(t, err, "daprd did not connect to sentry")

	addr := i.daprd.InternalGRPCAddress()
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		require.NoError(t, ln.Close())
	}
	require.Error(t, err, "internal gRPC port %s was free when daprd made its first outbound connection", addr)
}
