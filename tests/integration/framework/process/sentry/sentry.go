/*
Copyright 2023 The Dapr Authors
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

package sentry

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Sentry struct {
	exec     process.Interface
	freeport *util.FreePort

	bundle      ca.Bundle
	port        int
	healthzPort int
	metricsPort int
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, nil)
	require.NoError(t, err)

	fp := util.ReservePorts(t, 3)
	opts := options{
		bundle:      bundle,
		port:        fp.Port(t, 0),
		healthzPort: fp.Port(t, 1),
		metricsPort: fp.Port(t, 2),
		writeBundle: true,
		writeConfig: true,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"-log-level=" + "info",
		"-port=" + strconv.Itoa(opts.port),
		"-issuer-ca-filename=ca.crt",
		"-issuer-certificate-filename=issuer.crt",
		"-issuer-key-filename=issuer.key",
		"-metrics-port=" + strconv.Itoa(opts.metricsPort),
		"-healthz-port=" + strconv.Itoa(opts.healthzPort),
	}

	if opts.writeBundle {
		tmpDir := t.TempDir()
		caPath := filepath.Join(tmpDir, "ca.crt")
		issuerKeyPath := filepath.Join(tmpDir, "issuer.key")
		issuerCertPath := filepath.Join(tmpDir, "issuer.crt")

		for _, pair := range []struct {
			path string
			data []byte
		}{
			{caPath, opts.bundle.TrustAnchors},
			{issuerKeyPath, opts.bundle.IssKeyPEM},
			{issuerCertPath, opts.bundle.IssChainPEM},
		} {
			require.NoError(t, os.WriteFile(pair.path, pair.data, 0o600))
		}
		args = append(args, "-issuer-credentials="+tmpDir)
	} else {
		args = append(args, "-issuer-credentials="+t.TempDir())
	}

	if opts.kubeconfig != nil {
		args = append(args, "-kubeconfig="+*opts.kubeconfig)
	}

	if opts.trustDomain != nil {
		args = append(args, "-trust-domain="+*opts.trustDomain)
	}

	if opts.writeConfig {
		configPath := filepath.Join(t.TempDir(), "sentry-config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(opts.configuration), 0o600))
		args = append(args, "-config="+configPath)
	}

	return &Sentry{
		exec:        exec.New(t, binary.EnvValue("sentry"), args, opts.execOpts...),
		freeport:    fp,
		bundle:      opts.bundle,
		port:        opts.port,
		metricsPort: opts.metricsPort,
		healthzPort: opts.healthzPort,
	}
}

func (s *Sentry) Run(t *testing.T, ctx context.Context) {
	s.freeport.Free(t)
	s.exec.Run(t, ctx)
}

func (s *Sentry) Cleanup(t *testing.T) {
	s.exec.Cleanup(t)
}

func (s *Sentry) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", s.healthzPort), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusOK == resp.StatusCode
	}, time.Second*5, 100*time.Millisecond)
}

func (s *Sentry) CABundle() ca.Bundle {
	return s.bundle
}

func (s *Sentry) Port() int {
	return s.port
}

func (s *Sentry) MetricsPort() int {
	return s.metricsPort
}

func (s *Sentry) HealthzPort() int {
	return s.healthzPort
}

// DialGRPC dials the sentry using the given context and returns a grpc client
// connection.
func (s *Sentry) DialGRPC(t *testing.T, ctx context.Context, sentryID string) *grpc.ClientConn {
	bundle := s.CABundle()
	sentrySPIFFEID, err := spiffeid.FromString(sentryID)
	require.NoError(t, err)

	x509bundle, err := x509bundle.Parse(sentrySPIFFEID.TrustDomain(), bundle.TrustAnchors)
	require.NoError(t, err)
	transportCredentials := grpccredentials.TLSClientCredentials(x509bundle, tlsconfig.AuthorizeID(sentrySPIFFEID))

	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	defer cancel()
	conn, err := grpc.DialContext(
		ctx,
		fmt.Sprintf("127.0.0.1:%d", s.Port()),
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithReturnConnectionError(),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, conn.Close())
	})

	return conn
}
