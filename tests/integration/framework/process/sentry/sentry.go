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
	"crypto/rsa"
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

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/kit/ptr"
)

type Sentry struct {
	exec  process.Interface
	ports *ports.Ports

	bundle      *bundle.Bundle
	port        int
	healthzPort int
	metricsPort int
	oidcPort    *int
	trustDomain *string
	namespace   string

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Sentry {
	t.Helper()

	fp := ports.Reserve(t, 4)
	opts := options{
		port:        fp.Port(t),
		healthzPort: fp.Port(t),
		metricsPort: fp.Port(t),
		writeBundle: true,
		writeConfig: true,
		oidc: oidcOptions{
			serverListenPort: ptr.Of(fp.Port(t)),
		},
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
		// Generate key for X.509 certificates
		x509RootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		// Generate key for JWT signing
		jwtRootKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)

		x509bundle, err := bundle.GenerateX509(bundle.OptionsX509{
			X509RootKey:      x509RootKey,
			TrustDomain:      td,
			AllowedClockSkew: time.Second * 5,
			OverrideCATTL:    nil, // Use default CA TTL
		})
		require.NoError(t, err)
		jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
			JWTRootKey:  jwtRootKey,
			TrustDomain: td,
		})
		require.NoError(t, err)
		opts.bundle = &bundle.Bundle{
			X509: x509bundle,
			JWT:  jwtbundle,
		}
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
		jwtSigningKeyPath := filepath.Join(tmpDir, "jwt.key")
		jwksPath := filepath.Join(tmpDir, "jwks.json")

		for _, pair := range []struct {
			path string
			data []byte
		}{
			{caPath, opts.bundle.X509.TrustAnchors},
			{issuerKeyPath, opts.bundle.X509.IssKeyPEM},
			{issuerCertPath, opts.bundle.X509.IssChainPEM},
			{jwtSigningKeyPath, opts.bundle.JWT.SigningKeyPEM},
			{jwksPath, opts.bundle.JWT.JWKSJson},
		} {
			require.NoError(t, os.WriteFile(pair.path, pair.data, 0o600))
		}
		args = append(args, "-issuer-credentials="+tmpDir)
	} else {
		args = append(args, "-issuer-credentials="+tmpDir)
	}

	// Handle JWT options
	if opts.jwt.enabled {
		args = append(args, "-jwt-enabled=true")

		// Check if we should automatically set the JWT issuer to the OIDC server URL
		if opts.jwt.jwtIssuerFromOIDC && opts.oidc.enabled {
			require.Nil(t, opts.jwt.issuer, "jwtIssuer must be nil when issuerAutoOIDCPort is enabled")
			require.NotNil(t, opts.oidc.serverListenPort, "OIDC server port must be set when issuerAutoOIDCPort is enabled")

			// Build the JWT issuer URL using the OIDC server configuration
			oidcScheme := "https"
			if opts.oidc.tlsCertFile == nil || opts.oidc.tlsKeyFile == nil {
				oidcScheme = "http"
			}

			pathPrefix := ""
			if opts.oidc.pathPrefix != nil {
				pathPrefix = *opts.oidc.pathPrefix
			}

			jwtIssuer := fmt.Sprintf("%s://localhost:%d%s", oidcScheme, *opts.oidc.serverListenPort, pathPrefix)
			args = append(args, "-jwt-issuer="+jwtIssuer)
		} else if opts.jwt.issuer != nil {
			args = append(args, "-jwt-issuer="+*opts.jwt.issuer)
		}

		if opts.jwt.ttl != nil {
			args = append(args, "-jwt-ttl="+opts.jwt.ttl.String())
		}
		if opts.jwt.keyID != nil {
			args = append(args, "-jwt-key-id="+*opts.jwt.keyID)
		}
	} else {
		require.Nil(t, opts.jwt.issuer, "jwtIssuer must be nil when JWT is not enabled")
		require.False(t, opts.jwt.jwtIssuerFromOIDC, "jwtIssuerFromOIDC must be false when JWT is not enabled")
	}

	// Handle OIDC options
	if opts.oidc.enabled {
		args = append(args, "-oidc-enabled=true")

		if opts.oidc.serverListenPort != nil {
			args = append(args, "-oidc-server-listen-port="+strconv.Itoa(*opts.oidc.serverListenPort))
		}

		// When OIDC HTTP server is enabled, set the other OIDC options
		if opts.oidc.jwksURI != nil {
			args = append(args, "-oidc-jwks-uri="+*opts.oidc.jwksURI)
		}

		if opts.oidc.pathPrefix != nil {
			args = append(args, "-oidc-path-prefix="+*opts.oidc.pathPrefix)
		}

		if len(opts.oidc.allowedHosts) > 0 {
			args = append(args, "-oidc-allowed-hosts="+strings.Join(opts.oidc.allowedHosts, ","))
		}

		// Handle TLS files for OIDC HTTP server
		if opts.oidc.tlsCertFile == nil || opts.oidc.tlsKeyFile == nil {
			args = append(args, "-oidc-server-tls-enabled=false")
		}

		if opts.oidc.tlsCertFile != nil {
			args = append(args, "-oidc-server-tls-cert-file="+*opts.oidc.tlsCertFile)
		}

		if opts.oidc.tlsKeyFile != nil {
			args = append(args, "-oidc-server-tls-key-file="+*opts.oidc.tlsKeyFile)
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
		oidcPort:    opts.oidc.serverListenPort,
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
	require.NoError(t, os.WriteFile(taf, s.CABundle().X509.TrustAnchors, 0o600))
	return taf
}

func (s *Sentry) CABundle() bundle.Bundle {
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

func (s *Sentry) OIDCPort(t *testing.T) int {
	require.NotNil(t, s.oidcPort)
	return *s.oidcPort
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

	x509bundle, err := x509bundle.Parse(sentrySPIFFEID.TrustDomain(), bundle.X509.TrustAnchors)
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
		if err := conn.Close(); err != nil {
			if err != grpc.ErrClientConnClosing && //nolint:staticcheck
				!strings.Contains(err.Error(), "connection is closing") &&
				!strings.Contains(err.Error(), "already closed") {
				require.NoError(t, err, "Failed to close gRPC connection")
			}

			// Log the error but don't fail the test if connection is already closed
			t.Logf("Warning: gRPC connection close error (may be already closed): %v", err)
		}
	})

	return conn
}
