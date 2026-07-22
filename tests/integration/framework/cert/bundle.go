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
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	secpem "github.com/dapr/kit/crypto/pem"
)

// GenerateCABundle generates an X.509 + JWT CA bundle for the given trust
// domain whose root CA and issuer cert expire after ttl. A zero ttl uses the
// default CA TTL.
func GenerateCABundle(t *testing.T, trustDomain string, ttl time.Duration) bundle.Bundle {
	t.Helper()

	_, rootKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	opts := bundle.OptionsX509{
		X509RootKey:      rootKey,
		TrustDomain:      trustDomain,
		AllowedClockSkew: time.Second * 5,
	}
	if ttl > 0 {
		opts.OverrideCATTL = &ttl
	}

	x509bundle, err := bundle.GenerateX509(opts)
	require.NoError(t, err)
	jwtbundle, err := bundle.GenerateJWT(bundle.OptionsJWT{
		JWTRootKey:  jwtKey,
		TrustDomain: trustDomain,
	})
	require.NoError(t, err)

	return bundle.Bundle{X509: x509bundle, JWT: jwtbundle}
}

// DecodePEM decodes all certificates from PEM data.
func DecodePEM(t *testing.T, data []byte) []*x509.Certificate {
	t.Helper()
	certs, err := secpem.DecodePEMCertificates(data)
	require.NoError(t, err)
	require.NotEmpty(t, certs)
	return certs
}

// DecodePEMFile decodes all certificates from a PEM file.
func DecodePEMFile(t *testing.T, path string) []*x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return DecodePEM(t, data)
}
