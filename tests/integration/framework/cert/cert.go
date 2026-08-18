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

package cert

import (
	"context"
	"crypto/ed25519"
	"crypto/rsa"
	cryptotls "crypto/tls"
	"crypto/x509"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CaptureServingCert dials addr over TLS and returns the leaf certificate
// presented by the server. The handshake itself may fail when the server
// enforces mTLS and the empty client certificate is rejected; the leaf is
// captured before that via VerifyPeerCertificate, which runs on the client as
// soon as the server's certificate is received.
func CaptureServingCert(t *testing.T, ctx context.Context, addr string) *x509.Certificate {
	t.Helper()

	var captured atomic.Pointer[x509.Certificate]
	cfg := &cryptotls.Config{
		//nolint:gosec
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return nil
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			captured.Store(cert)
			return nil
		},
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		raw, err := dialer.DialContext(dialCtx, "tcp", addr)
		if !assert.NoError(c, err) {
			return
		}
		defer raw.Close()

		tlsConn := cryptotls.Client(raw, cfg)
		_ = tlsConn.HandshakeContext(dialCtx)
		_ = tlsConn.Close()

		assert.NotNil(c, captured.Load(), "no peer certificate captured from %s", addr)
	}, 30*time.Second, 10*time.Millisecond)

	cert := captured.Load()
	require.NotNil(t, cert, "no peer certificate captured from %s", addr)
	return cert
}

// AssertEd25519ServingCert asserts that the TLS server at addr presents a leaf
// certificate whose public key is an Ed25519 key.
func AssertEd25519ServingCert(t *testing.T, ctx context.Context, addr string) {
	t.Helper()
	cert := CaptureServingCert(t, ctx, addr)
	_, ok := cert.PublicKey.(ed25519.PublicKey)
	assert.True(t, ok, "%s serving cert public key should be ed25519.PublicKey, got %T", addr, cert.PublicKey)
}

// AssertRSAServingCert asserts that the TLS server at addr presents a leaf
// certificate whose public key is an RSA key.
func AssertRSAServingCert(t *testing.T, ctx context.Context, addr string) {
	t.Helper()
	cert := CaptureServingCert(t, ctx, addr)
	_, ok := cert.PublicKey.(*rsa.PublicKey)
	assert.True(t, ok, "%s serving cert public key should be *rsa.PublicKey, got %T", addr, cert.PublicKey)
}
