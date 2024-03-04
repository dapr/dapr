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

	bundle      *ca.Bundle
	port        int
	healthzPort int
	metricsPort int
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	fp := util.ReservePorts(t, 3)
	opts := options{
		port:        fp.Port(t, 0),
		healthzPort: fp.Port(t, 1),
		metricsPort: fp.Port(t, 2),
		writeBundle: true,
		writeConfig: true,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	// Only generate a bundle if one was not provided.
	if opts.bundle == nil {
		td := spiffeid.RequireTrustDomainFromString("default").String()
		if opts.trustDomain != nil {
			td = *opts.trustDomain
		}
		pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		bundle, err := ca.GenerateBundle(pk, td, time.Second*5, nil)
		require.NoError(t, err)
		opts.bundle = &bundle
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

	if opts.namespace != nil {
		opts.execOpts = append(opts.execOpts, exec.WithEnvVars(t, "NAMESPACE", *opts.namespace))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", s.healthzPort), nil)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Do(req)
		//nolint:testifylint
		if assert.NoError(c, err) {
			defer resp.Body.Close()
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*20, 100*time.Millisecond)
}

func (s *Sentry) TrustAnchorsFile(t *testing.T) string {
	t.Helper()
	taf := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taf, s.CABundle().TrustAnchors, 0o600))
	return taf
}

func (s *Sentry) CABundle() ca.Bundle {
	return *s.bundle
}

func (s *Sentry) Port() int {
	return s.port
}

func (s *Sentry) Address() string {
	return "localhost:" + strconv.Itoa(s.Port())
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
		require.NoError(t, conn.Close())
	})

	return conn
}
