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

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

const (
	errInfoType = "type.googleapis.com/google.rpc.ErrorInfo"
)

func init() {
	suite.Register(new(secretErrors))
}

type secretErrors struct {
	daprdNoStore   *procdaprd.Daprd
	daprdWithStore *procdaprd.Daprd
}

func (e *secretErrors) Setup(t *testing.T) []framework.Option {
	secretFile := filepath.Join(t.TempDir(), "secrets.json")
	require.NoError(t, os.WriteFile(secretFile, []byte(`{"mykey":"myvalue"}`), 0o600))

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myconfig
spec:
  secrets:
    scopes:
    - storeName: mysecretstore
      defaultAccess: deny
      allowedSecrets: ["mykey"]
`), 0o600))

	e.daprdNoStore = procdaprd.New(t)
	e.daprdWithStore = procdaprd.New(t,
		procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mysecretstore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`, secretFile)),
		procdaprd.WithConfigs(configFile),
	)

	return []framework.Option{
		framework.WithProcesses(e.daprdNoStore, e.daprdWithStore),
	}
}

func (e *secretErrors) Run(t *testing.T, ctx context.Context) {
	e.daprdNoStore.WaitUntilRunning(t, ctx)
	e.daprdWithStore.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	t.Run("secret store not configured", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/secrets/mysecretstore/mykey", e.daprdNoStore.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_SECRET_STORES_NOT_CONFIGURED", data["errorCode"])
		assert.Equal(t, "secret store is not configured", data["message"])
		assertErrInfo(t, data, "ERR_SECRET_STORES_NOT_CONFIGURED")
	})

	t.Run("secret store not found", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/secrets/nonexistent-store/mykey", e.daprdWithStore.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_SECRET_STORE_NOT_FOUND", data["errorCode"])
		assert.Equal(t, "failed finding secret store with key nonexistent-store", data["message"])
		assertErrInfo(t, data, "ERR_SECRET_STORE_NOT_FOUND")
	})

	t.Run("secret permission denied", func(t *testing.T) {
		url := fmt.Sprintf("http://localhost:%d/v1.0/secrets/mysecretstore/denied-key", e.daprdWithStore.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		require.Equal(t, http.StatusForbidden, resp.StatusCode)

		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var data map[string]any
		require.NoError(t, json.Unmarshal(b, &data))

		assert.Equal(t, "ERR_PERMISSION_DENIED", data["errorCode"])
		assertErrInfo(t, data, "ERR_PERMISSION_DENIED")
	})
}

func assertErrInfo(t *testing.T, data map[string]any, expectedReason string) {
	t.Helper()
	details, ok := data["details"].([]any)
	require.True(t, ok, "details field missing or not an array")
	require.NotEmpty(t, details)

	for _, d := range details {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if dm["@type"] == errInfoType {
			assert.Equal(t, expectedReason, dm["reason"])
			return
		}
	}
	t.Errorf("ErrorInfo detail with type %s not found in response", errInfoType)
}
