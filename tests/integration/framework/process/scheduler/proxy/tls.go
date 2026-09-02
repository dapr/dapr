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

package proxy

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"

	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
)

// Option configures the proxy.
type Option func(*Proxy)

// WithSentry makes the proxy a full mTLS member of the control plane. It
// serves daprd with a leaf certificate minted from the sentry CA carrying
// the scheduler control-plane SPIFFE identity, and dials the upstream
// scheduler presenting the identity of the daprd app whose requests it
// forwards (the scheduler authorizes each request against the caller's
// SPIFFE identity, so the proxy must impersonate the app, which therefore
// must run with the given fixed app ID). The upstream scheduler must be
// built with scheduler.WithSentry and an ID matching its certificate DNS
// names.
func WithSentry(t *testing.T, sen *sentry.Sentry, namespace, appID string) Option {
	t.Helper()

	x509bundle := sen.CABundle().X509

	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(x509bundle.TrustAnchors))

	td := spiffeid.RequireTrustDomainFromString(sen.TrustDomain(t))

	mint := func(id spiffeid.ID, dns []string) tls.Certificate {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(time.Hour),
			DNSNames:     dns,
			URIs:         []*url.URL{id.URL()},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, x509bundle.IssChain[0], key.Public(), x509bundle.IssKey)
		require.NoError(t, err)
		return tls.Certificate{
			Certificate: [][]byte{der, x509bundle.IssChain[0].Raw},
			PrivateKey:  key,
		}
	}

	// daprd validates the scheduler server against the control-plane SPIFFE
	// identity spiffe://<td>/ns/default/dapr-scheduler; the DNS SAN mirrors
	// the conventional scheduler ID for hostname based verifiers.
	serverLeaf := mint(
		spiffeid.RequireFromSegments(td, "ns", "default", "dapr-scheduler"),
		[]string{"dapr-scheduler-server-0"},
	)
	clientLeaf := mint(
		spiffeid.RequireFromSegments(td, "ns", namespace, appID),
		nil,
	)

	return func(p *Proxy) {
		p.serverCreds = credentials.NewTLS(&tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{serverLeaf},
			ClientCAs:    pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
		})
		p.upstreamTLS = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{clientLeaf},
			// The upstream scheduler presents a SPIFFE SVID, which standard
			// TLS verification rejects (no hostname oriented EKU chain). The
			// proxy owns both loopback endpoints in-test, so skip server
			// verification while still presenting our client SVID.
			//nolint:gosec
			InsecureSkipVerify: true,
		}
	}
}
