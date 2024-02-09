/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(encryption))
}

type encryption struct {
	daprd *procdaprd.Daprd
}

func (e *encryption) Setup(t *testing.T) []framework.Option {
	tmp := t.TempDir()
	secretsFile := filepath.Join(tmp, "secrets.json")
	secretsJson := `{ "key":  "5ca12365a9c1cc981090331f7eec683d"}`

	secretStore := fmt.Sprintf(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: secretstore
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
  `, secretsFile)

	stateStore := `apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: primaryEncryptionKey
    secretKeyRef:
      name: key
auth:
  secretStore: secretstore
`

	os.WriteFile(secretsFile, []byte(secretsJson), 0o600)
	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(secretStore, stateStore))

	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *encryption) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)
	postURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore", e.daprd.HTTPPort())
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/state/mystore/key1", e.daprd.HTTPPort())

	t.Run("valid encrypted save", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(`[{"key": "key1", "value": "value1"}]`))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Empty(t, string(body))
	})

	t.Run("valid encrypted get", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.Equal(t, "value1", string(body))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	})
}
