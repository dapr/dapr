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

package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
)

var writePerm os.FileMode

func init() {
	writePerm = 0o600
	if runtime.GOOS == "windows" {
		writePerm = 0o666
	}
}

func TestSelfhosted_store(t *testing.T) {
	t.Run("storing file should write to disk with correct permissions", func(t *testing.T) {
		rootFile := filepath.Join(t.TempDir(), "root.pem")
		issuerFile := filepath.Join(t.TempDir(), "issuer.pem")
		keyFile := filepath.Join(t.TempDir(), "key.pem")

		s := &selfhosted{
			config: config.Config{
				RootCertPath:   rootFile,
				IssuerCertPath: issuerFile,
				IssuerKeyPath:  keyFile,
			},
		}

		require.NoError(t, s.store(t.Context(), bundle.Bundle{
			X509: &bundle.X509{
				TrustAnchors: []byte("root"),
				IssChainPEM:  []byte("issuer"),
				IssKeyPEM:    []byte("key"),
			},
		}))

		require.FileExists(t, rootFile)
		require.FileExists(t, issuerFile)
		require.FileExists(t, keyFile)

		info, err := os.Stat(rootFile)
		require.NoError(t, err)
		assert.Equal(t, writePerm, info.Mode().Perm())

		info, err = os.Stat(issuerFile)
		require.NoError(t, err)
		assert.Equal(t, writePerm, info.Mode().Perm())

		info, err = os.Stat(keyFile)
		require.NoError(t, err)
		assert.Equal(t, writePerm, info.Mode().Perm())

		b, err := os.ReadFile(rootFile)
		require.NoError(t, err)
		assert.Equal(t, "root", string(b))

		b, err = os.ReadFile(issuerFile)
		require.NoError(t, err)
		assert.Equal(t, "issuer", string(b))

		b, err = os.ReadFile(keyFile)
		require.NoError(t, err)
		assert.Equal(t, "key", string(b))
	})
}

func TestSelfhosted_get(t *testing.T) {
	rootPEM, rootCrt, _, rootPK := genCrt(t, "root", nil, nil)
	intPEM, intCrt, intPKPEM, intPK := genCrt(t, "int", rootCrt, rootPK)

	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwtKeyDer, err := x509.MarshalPKCS8PrivateKey(signingKey)
	require.NoError(t, err)

	signingKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtKeyDer})

	jwksBytes := createJWKS(t, signingKey, "kid")
	jwks, err := jwk.Parse(jwksBytes)
	require.NoError(t, err)

	tests := map[string]struct {
		rootFile  *[]byte
		issuer    *[]byte
		key       *[]byte
		jwtKey    *[]byte
		jwksData  *[]byte
		expBundle bundle.Bundle
		expErr    bool
	}{
		"if no files exist, return not ok": {
			rootFile:  nil,
			issuer:    nil,
			key:       nil,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    false,
		},
		"if root file doesn't exist, return not ok": {
			rootFile:  nil,
			issuer:    &intPEM,
			key:       &intPKPEM,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    false,
		},
		"if issuer file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    nil,
			key:       &intPKPEM,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    false,
		},
		"if issuer key file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    &intPEM,
			key:       nil,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    false,
		},
		"if failed to verify CA bundle, return error": {
			rootFile:  &intPEM,
			issuer:    &intPEM,
			key:       &intPKPEM,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    true,
		},
		"if all x509 files exist but no JWT files, return certs and expect to generate JWT": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			jwtKey:   nil,
			jwksData: nil,
			expBundle: bundle.Bundle{
				X509: &bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if jwt key exists but jwks doesn't, expect to generate JWT": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			jwksData: nil,
			expBundle: bundle.Bundle{
				X509: &bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if jwks exists but jwt key doesn't, expect nil": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			jwtKey:   nil,
			jwksData: &jwksBytes,
			expBundle: bundle.Bundle{
				X509: &bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
			},
			expErr: false,
		},
		"if jwt key is invalid, expect error": {
			rootFile:  &rootPEM,
			issuer:    &intPEM,
			key:       &intPKPEM,
			jwtKey:    &intPKPEM, // Using an invalid JWT key
			jwksData:  &jwksBytes,
			expBundle: bundle.Bundle{},
			expErr:    true,
		},
		"if jwks is invalid, expect error": {
			rootFile:  &rootPEM,
			issuer:    &intPEM,
			key:       &intPKPEM,
			jwtKey:    &signingKeyPEM,
			jwksData:  &intPKPEM, // Using invalid JWKS data
			expBundle: bundle.Bundle{},
			expErr:    true,
		},
		"if all files exist including valid JWT components, return complete bundle": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			jwtKey:   &signingKeyPEM,
			jwksData: &jwksBytes,
			expBundle: bundle.Bundle{
				X509: &bundle.X509{
					TrustAnchors: rootPEM,
					IssChainPEM:  intPEM,
					IssKeyPEM:    intPKPEM,
					IssChain:     []*x509.Certificate{intCrt},
					IssKey:       intPK,
				},
				JWT: &bundle.JWT{
					SigningKey:    signingKey,
					SigningKeyPEM: signingKeyPEM,
					JWKSJson:      jwksBytes,
					JWKS:          jwks,
				},
			},
			expErr: false,
		},
		"if only JWT files exist but no x509 files, expect to generate x509": {
			rootFile: nil,
			issuer:   nil,
			key:      nil,
			jwtKey:   &signingKeyPEM,
			jwksData: &jwksBytes,
			expBundle: bundle.Bundle{
				JWT: &bundle.JWT{
					SigningKey:    signingKey,
					SigningKeyPEM: signingKeyPEM,
					JWKSJson:      jwksBytes,
					JWKS:          jwks,
				},
			},
			expErr: false,
		},
		"if no files exist at all, expect to generate both x509 and JWT": {
			rootFile:  nil,
			issuer:    nil,
			key:       nil,
			jwtKey:    nil,
			jwksData:  nil,
			expBundle: bundle.Bundle{},
			expErr:    false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Helper()

			dir := t.TempDir()
			rootFile := filepath.Join(dir, "root.pem")
			issuerFile := filepath.Join(dir, "issuer.pem")
			keyFile := filepath.Join(dir, "key.pem")
			jwtKeyFile := filepath.Join(dir, "jwt.key")
			jwksFile := filepath.Join(dir, "jwks.json")
			s := &selfhosted{
				config: config.Config{
					RootCertPath:   rootFile,
					IssuerCertPath: issuerFile,
					IssuerKeyPath:  keyFile,
					JWT: config.ConfigJWT{
						SigningKeyPath: jwtKeyFile,
						JWKSPath:       jwksFile,
						TTL:            config.DefaultJWTTTL,
					},
				},
			}

			if test.rootFile != nil {
				require.NoError(t, os.WriteFile(rootFile, *test.rootFile, writePerm))
			}
			if test.issuer != nil {
				require.NoError(t, os.WriteFile(issuerFile, *test.issuer, writePerm))
			}
			if test.key != nil {
				require.NoError(t, os.WriteFile(keyFile, *test.key, writePerm))
			}
			if test.jwtKey != nil {
				require.NoError(t, os.WriteFile(jwtKeyFile, *test.jwtKey, writePerm))
			}
			if test.jwksData != nil {
				require.NoError(t, os.WriteFile(jwksFile, *test.jwksData, writePerm))
			}

			bundle, err := s.get(t.Context())
			assert.Equal(t, test.expErr, err != nil, "expected error: %v, but got %v", test.expErr, err)
			bundlesEqual(t, test.expBundle, bundle)
		})
	}
}
