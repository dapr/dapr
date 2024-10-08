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
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/config"
)

var writePerm os.FileMode

func init() {
	writePerm = 0o600
	if runtime.GOOS == "windows" {
		writePerm = 0o666
	}
}

func TestSelhosted_store(t *testing.T) {
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

		require.NoError(t, s.store(context.Background(), Bundle{
			TrustAnchors: []byte("root"),
			IssChainPEM:  []byte("issuer"),
			IssKeyPEM:    []byte("key"),
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

	tests := map[string]struct {
		rootFile  *[]byte
		issuer    *[]byte
		key       *[]byte
		expBundle Bundle
		expOk     bool
		expErr    bool
	}{
		"if no files exist, return not ok": {
			rootFile:  nil,
			issuer:    nil,
			key:       nil,
			expBundle: Bundle{},
			expOk:     false,
			expErr:    false,
		},
		"if root file doesn't exist, return not ok": {
			rootFile:  nil,
			issuer:    &intPEM,
			key:       &intPKPEM,
			expBundle: Bundle{},
			expOk:     false,
			expErr:    false,
		},
		"if issuer file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    nil,
			key:       &intPKPEM,
			expBundle: Bundle{},
			expOk:     false,
			expErr:    false,
		},
		"if issuer key file doesn't exist, return not ok": {
			rootFile:  &rootPEM,
			issuer:    &intPEM,
			key:       nil,
			expBundle: Bundle{},
			expOk:     false,
			expErr:    false,
		},
		"if failed to verify CA bundle, return error": {
			rootFile:  &intPEM,
			issuer:    &intPEM,
			key:       &intPKPEM,
			expBundle: Bundle{},
			expOk:     false,
			expErr:    true,
		},
		"if all files exist, return certs": {
			rootFile: &rootPEM,
			issuer:   &intPEM,
			key:      &intPKPEM,
			expBundle: Bundle{
				TrustAnchors: rootPEM,
				IssChainPEM:  intPEM,
				IssKeyPEM:    intPKPEM,
				IssChain:     []*x509.Certificate{intCrt},
				IssKey:       intPK,
			},
			expOk:  true,
			expErr: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			rootFile := filepath.Join(dir, "root.pem")
			issuerFile := filepath.Join(dir, "issuer.pem")
			keyFile := filepath.Join(dir, "key.pem")
			s := &selfhosted{
				config: config.Config{
					RootCertPath:   rootFile,
					IssuerCertPath: issuerFile,
					IssuerKeyPath:  keyFile,
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

			got, ok, err := s.get(context.Background())
			assert.Equal(t, test.expBundle, got)
			assert.Equal(t, test.expOk, ok)
			assert.Equal(t, test.expErr, err != nil, "%v", err)
		})
	}
}
