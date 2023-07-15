/*
Copyright 2022 The Dapr Authors
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

package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/ptr"
)

func TestAPIToken(t *testing.T) {
	t.Run("existing token", func(t *testing.T) {
		/* #nosec */
		token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJPbmxpbmUgSldUIEJ1aWxkZXIiLCJpYXQiOjE1OTA1NTQ1NzMsImV4cCI6MTYyMjA5MDU3MywiYXVkIjoid3d3LmV4YW1wbGUuY29tIiwic3ViIjoianJvY2tldEBleGFtcGxlLmNvbSIsIkdpdmVuTmFtZSI6IkpvaG5ueSIsIlN1cm5hbWUiOiJSb2NrZXQiLCJFbWFpbCI6Impyb2NrZXRAZXhhbXBsZS5jb20iLCJSb2xlIjpbIk1hbmFnZXIiLCJQcm9qZWN0IEFkbWluaXN0cmF0b3IiXX0.QLFl8ZqC48DOsT7SmXA794nivmqGgylzjrUu6JhXPW4"
		t.Setenv(consts.APITokenEnvVar, token)

		apitoken := GetAPIToken()
		assert.Equal(t, token, apitoken)
	})

	t.Run("non-existent token", func(t *testing.T) {
		token := GetAPIToken()
		assert.Equal(t, "", token)
	})
}

func TestAppToken(t *testing.T) {
	t.Run("existing token", func(t *testing.T) {
		/* #nosec */
		token := "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJPbmxpbmUgSldUIEJ1aWxkZXIiLCJpYXQiOjE1OTA1NTQ1NzMsImV4cCI6MTYyMjA5MDU3MywiYXVkIjoid3d3LmV4YW1wbGUuY29tIiwic3ViIjoianJvY2tldEBleGFtcGxlLmNvbSIsIkdpdmVuTmFtZSI6IkpvaG5ueSIsIlN1cm5hbWUiOiJSb2NrZXQiLCJFbWFpbCI6Impyb2NrZXRAZXhhbXBsZS5jb20iLCJSb2xlIjpbIk1hbmFnZXIiLCJQcm9qZWN0IEFkbWluaXN0cmF0b3IiXX0.QLFl8ZqC48DOsT7SmXA794nivmqGgylzjrUu6JhXPW4"
		t.Setenv(consts.AppAPITokenEnvVar, token)

		apitoken := GetAppToken()
		assert.Equal(t, token, apitoken)
	})

	t.Run("non-existent token", func(t *testing.T) {
		token := GetAppToken()
		assert.Equal(t, "", token)
	})
}

func TestGetKubernetesIdentityToken(t *testing.T) {
	tests := map[string]struct {
		kubeToken  *string
		boundToken *string
		exp        string
		expErr     bool
	}{
		"if neither token is present, expect an error": {
			kubeToken:  nil,
			boundToken: nil,
			exp:        "",
			expErr:     true,
		},
		"if only kube token is present, expect error": {
			kubeToken:  ptr.Of("kube-token"),
			boundToken: nil,
			exp:        "",
			expErr:     true,
		},
		"if only boundToken, expect bound token": {
			kubeToken:  nil,
			boundToken: ptr.Of("bound-token"),
			exp:        "bound-token",
			expErr:     false,
		},
		"if both tokens are present, expect bound token": {
			kubeToken:  ptr.Of("kube-token"),
			boundToken: ptr.Of("bound-token"),
			exp:        "bound-token",
			expErr:     false,
		},
	}

	origFS := rootFS
	t.Cleanup(func() {
		rootFS = origFS
	})

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			rootFS = t.TempDir()
			for _, tt := range []struct {
				path  string
				token *string
			}{
				{path: "/var/run/secrets/kubernetes.io/serviceaccount/token", token: test.kubeToken},
				{path: "/var/run/secrets/dapr.io/sentrytoken/token", token: test.boundToken},
			} {
				if tt.token != nil {
					path := filepath.Join(rootFS, tt.path)
					require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
					require.NoError(t, os.WriteFile(path, []byte(*tt.token), 0o600))
				}
			}

			got, err := getKubernetesIdentityToken()
			assert.Equal(t, test.expErr, err != nil, err)
			assert.Equal(t, test.exp, got)
		})
	}
}
