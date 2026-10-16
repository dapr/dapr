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

package sighup

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/exec/kill"
	grpcsentry "github.com/dapr/dapr/tests/integration/framework/process/grpc/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(interrupt))
}

type interrupt struct {
	daprd  *daprd.Daprd
	logOut *log.Log

	hold    atomic.Bool
	held    chan struct{}
	heldOne sync.Once
}

func (i *interrupt) Setup(t *testing.T) []framework.Option {
	i.held = make(chan struct{})

	snt := sentry.New(t)
	td := spiffeid.RequireTrustDomainFromString(snt.TrustDomain(t))
	sentryID := spiffeid.RequireFromSegments(td, "ns", snt.Namespace(), "dapr-sentry")
	anchors, err := x509bundle.Parse(td, snt.CABundle().X509.TrustAnchors)
	require.NoError(t, err)
	conn, err := grpc.NewClient(snt.Address(), grpc.WithTransportCredentials(
		grpccredentials.TLSClientCredentials(anchors, tlsconfig.AuthorizeID(sentryID)),
	))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	ca := sentryv1pb.NewCAClient(conn)

	fake := grpcsentry.New(t,
		grpcsentry.WithCABundle(snt.CABundle()),
		grpcsentry.WithSignCertificateFn(func(ctx context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
			if i.hold.Load() {
				i.heldOne.Do(func() { close(i.held) })
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return ca.SignCertificate(ctx, req, grpc.WaitForReady(true))
		}),
	)

	i.logOut = log.New()
	i.daprd = daprd.New(t,
		daprd.WithSentry(t, snt),
		daprd.WithSentryAddress(fake.Address(t)),
		daprd.WithExecOptions(exec.WithStdout(i.logOut), exec.WithStderr(i.logOut)),
	)

	return []framework.Option{
		framework.WithProcesses(snt, fake, i.daprd),
	}
}

func (i *interrupt) Run(t *testing.T, ctx context.Context) {
	i.daprd.WaitUntilRunning(t, ctx)
	i.hold.Store(true)

	i.daprd.SignalHUP(t)
	select {
	case <-i.held:
	case <-ctx.Done():
		require.Fail(t, "the restarted runtime never asked sentry for its identity")
	}

	// Cleanup interrupts daprd and waits for its output to be read.
	i.daprd.Cleanup(t)
	assert.True(t, i.logOut.Contains(kill.HUPLogLine))
}
