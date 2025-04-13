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
	"strings"
	"sync"
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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Sentry struct {
	exec  process.Interface
	ports *ports.Ports

	bundle      *ca.Bundle
	port        int
	healthzPort int
	metricsPort int
	trustDomain *string
	namespace   string

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	fp := ports.Reserve(t, 3)
	opts := options{
		port:        fp.Port(t),
		healthzPort: fp.Port(t),
		metricsPort: fp.Port(t),
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
		"-metrics-listen-address=127.0.0.1",
		"-healthz-port=" + strconv.Itoa(opts.healthzPort),
		"-healthz-listen-address=127.0.0.1",
		"-listen-address=127.0.0.1",
	}

	tmpDir := t.TempDir()

	if opts.writeBundle {
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
		args = append(args, "-issuer-credentials="+tmpDir)
	}

	// Handle JWT options
	if opts.enableJWT {
		args = append(args, "-enable-jwt=true")

		// Check if JWT signing key file is provided
		if opts.jwtSigningKeyFile != nil {
			args = append(args, "-jwt-key-filename="+filepath.Base(*opts.jwtSigningKeyFile))

			// Copy the JWT signing key to the credentials directory
			jwtKeyDest := filepath.Join(tmpDir, filepath.Base(*opts.jwtSigningKeyFile))
			jwtKeyData, err := os.ReadFile(*opts.jwtSigningKeyFile)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(jwtKeyDest, jwtKeyData, 0o600))
		} else {
			// Use default JWT key filename
			args = append(args, "-jwt-key-filename=jwt.key")
		}

		// Check if JWKS file is provided
		if opts.jwksFile != nil {
			args = append(args, "-jwks-filename="+filepath.Base(*opts.jwksFile))

			// Copy the JWKS file to the credentials directory
			jwksDest := filepath.Join(tmpDir, filepath.Base(*opts.jwksFile))
			jwksData, err := os.ReadFile(*opts.jwksFile)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(jwksDest, jwksData, 0o600))
		} else {
			// Use default JWKS filename
			args = append(args, "-jwks-filename=jwks.json")
		}
	}

	// Handle OIDC options
	if opts.oidcHTTPPort != nil {
		args = append(args, "-oidc-http-port="+strconv.Itoa(*opts.oidcHTTPPort))

		// When OIDC HTTP server is enabled, set the other OIDC options
		if opts.oidcJWKSURI != nil {
			args = append(args, "-oidc-jwks-uri="+*opts.oidcJWKSURI)
		}

		if opts.oidcPathPrefix != nil {
			args = append(args, "-oidc-path-prefix="+*opts.oidcPathPrefix)
		}

		if len(opts.oidcDomains) > 0 {
			args = append(args, "-oidc-allowed-hosts="+strings.Join(opts.oidcDomains, ","))
		}

		// Handle TLS files for OIDC HTTP server
		if opts.oidcTLSCertFile != nil {
			args = append(args, "-oidc-tls-cert-file="+*opts.oidcTLSCertFile)
		}

		if opts.oidcTLSKeyFile != nil {
			args = append(args, "-oidc-tls-key-file="+*opts.oidcTLSKeyFile)
		}
	}

	if opts.kubeconfig != nil {
		args = append(args, "-kubeconfig="+*opts.kubeconfig)
	}

	if opts.trustDomain != nil {
		args = append(args, "-trust-domain="+*opts.trustDomain)
	}

	if opts.mode != nil {
		args = append(args, "-mode="+*opts.mode)
	}

	if opts.writeConfig {
		configPath := filepath.Join(t.TempDir(), "sentry-config.yaml")
		require.NoError(t, os.WriteFile(configPath, []byte(opts.configuration), 0o600))
		args = append(args, "-config="+configPath)
	}

	ns := "default"
	if opts.namespace != nil {
		opts.execOpts = append(opts.execOpts, exec.WithEnvVars(t, "NAMESPACE", *opts.namespace))
		ns = *opts.namespace
	}

	return &Sentry{
		exec:        exec.New(t, binary.EnvValue("sentry"), args, opts.execOpts...),
		ports:       fp,
		bundle:      opts.bundle,
		port:        opts.port,
		metricsPort: opts.metricsPort,
		healthzPort: opts.healthzPort,
		trustDomain: opts.trustDomain,
		namespace:   ns,
	}
}

func (s *Sentry) Run(t *testing.T, ctx context.Context) {
	s.runOnce.Do(func() {
		s.ports.Free(t)
		s.exec.Run(t, ctx)
	})
}

func (s *Sentry) Cleanup(t *testing.T) {
	s.cleanupOnce.Do(func() {
		s.exec.Cleanup(t)
	})
}

func (s *Sentry) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := client.HTTP(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", s.healthzPort), nil)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Do(req)
		if assert.NoError(c, err) {
			defer resp.Body.Close()
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*20, 10*time.Millisecond)
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

func (s *Sentry) Namespace() string {
	return s.namespace
}

func (s *Sentry) TrustDomain(t *testing.T) string {
	if s.trustDomain == nil {
		return "localhost"
	}
	return *s.trustDomain
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
	//nolint:staticcheck
	conn, err := grpc.DialContext(
		ctx,
		fmt.Sprintf("127.0.0.1:%d", s.Port()),
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithReturnConnectionError(), //nolint:staticcheck
		grpc.WithBlock(),                 //nolint:staticcheck
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, conn.Close())
	})

	return conn
}
