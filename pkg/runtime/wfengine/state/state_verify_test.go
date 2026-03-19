/*
Copyright 2024 The Dapr Authors
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

package state

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func generateTestCert(t *testing.T, spiffeID string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	uri, err := url.Parse(spiffeID)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		URIs:         []*url.URL{uri},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return certDER
}

func generateTestCertNoURI(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return certDER
}

func TestVerifyCertAppIdentity_ValidMatch(t *testing.T) {
	t.Parallel()

	certDER := generateTestCert(t, "spiffe://example.com/ns/default/myapp")
	err := verifyCertAppIdentity(certDER, "myapp")
	assert.NoError(t, err)
}

func TestVerifyCertAppIdentity_WrongAppID(t *testing.T) {
	t.Parallel()

	certDER := generateTestCert(t, "spiffe://example.com/ns/default/app-a")
	err := verifyCertAppIdentity(certDER, "app-b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestVerifyCertAppIdentity_EmptyCertChain(t *testing.T) {
	t.Parallel()

	err := verifyCertAppIdentity([]byte{}, "myapp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestVerifyCertAppIdentity_InvalidDER(t *testing.T) {
	t.Parallel()

	err := verifyCertAppIdentity([]byte{0xDE, 0xAD, 0xBE, 0xEF}, "myapp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestVerifyCertAppIdentity_NoURISAN(t *testing.T) {
	t.Parallel()

	certDER := generateTestCertNoURI(t)
	err := verifyCertAppIdentity(certDER, "myapp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SPIFFE ID")
}

func TestVerifyCertAppIdentity_DeepPath(t *testing.T) {
	t.Parallel()

	certDER := generateTestCert(t, "spiffe://example.com/ns/prod/region/us-east/myapp")
	err := verifyCertAppIdentity(certDER, "myapp")
	assert.NoError(t, err)
}
