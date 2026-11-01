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
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
)

type testCA struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

func (ca *testCA) leaf(t *testing.T, id spiffeid.ID) [][]byte {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{id.URL()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	require.NoError(t, err)
	return [][]byte{der}
}

func Test_verifySPIFFEPeer(t *testing.T) {
	t.Parallel()

	td := spiffeid.RequireTrustDomainFromString("public")
	scheduler := spiffeid.RequireFromSegments(td, "ns", "default", "dapr-scheduler")
	ca := newTestCA(t)

	require.NoError(t, verifySPIFFEPeer(ca.leaf(t, scheduler), ca.pool, scheduler))
	require.ErrorContains(t, verifySPIFFEPeer(ca.leaf(t, spiffeid.RequireFromSegments(td, "ns", "default", "impostor")), ca.pool, scheduler), "unexpected peer identity")
	require.Error(t, verifySPIFFEPeer(newTestCA(t).leaf(t, scheduler), ca.pool, scheduler), "a leaf from another CA must not verify")
	require.Error(t, verifySPIFFEPeer(nil, ca.pool, scheduler))
}
