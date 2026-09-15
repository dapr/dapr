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

package trustanchors

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(env))
}

// env tests that daprd consumes the trust anchors file path from the
// DAPR_TRUST_ANCHORS_FILE environment variable, and that it takes priority
// over the deprecated DAPR_TRUST_ANCHORS literal PEM environment variable.
type env struct {
	daprd  *daprd.Daprd
	sentry *sentry.Sentry
}

func (e *env) Setup(t *testing.T) []framework.Option {
	e.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(taFile, e.sentry.CABundle().X509.TrustAnchors, 0o600))

	e.daprd = daprd.New(t,
		daprd.WithAppID("my-app"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_TRUST_ANCHORS_FILE", taFile,
			// The legacy literal PEM env var is deliberately garbage. If daprd
			// wrongly consumed it over the file source, security initialization
			// would fail and mTLS connections would never succeed.
			"DAPR_TRUST_ANCHORS", "not-a-pem",
		)),
		daprd.WithSentryAddress(e.sentry.Address()),
		daprd.WithEnableMTLS(true),
	)

	return []framework.Option{
		framework.WithProcesses(e.sentry, e.daprd),
	}
}

func (e *env) Run(t *testing.T, ctx context.Context) {
	e.sentry.WaitUntilRunning(t, ctx)
	e.daprd.WaitUntilRunning(t, ctx)

	sctx, cancel := context.WithCancel(ctx)

	secProv, err := security.New(sctx, security.Options{
		SentryAddress:           e.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            e.sentry.CABundle().X509.TrustAnchors,
		AppID:                   "client",
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
	})
	require.NoError(t, err)

	secProvErr := make(chan error)
	go func() {
		secProvErr <- secProv.Run(sctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-time.After(time.Second * 5):
			t.Fatal("timed out waiting for security provider to stop")
		case cerr := <-secProvErr:
			require.NoError(t, cerr)
		}
	})

	sec, err := secProv.Handler(sctx)
	require.NoError(t, err)

	myAppID, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "my-app")
	require.NoError(t, err)

	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, e.daprd.InternalGRPCAddress(), sec.GRPCDialOptionMTLS(myAppID),
		grpc.WithReturnConnectionError())
	require.NoError(t, err)
	conn.Connect()
	assert.Equal(t, connectivity.Ready, conn.GetState())
	require.NoError(t, conn.Close())
}
