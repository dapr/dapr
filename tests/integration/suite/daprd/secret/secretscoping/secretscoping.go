/*
Copyright 2025 The Dapr Authors
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

package secretscoping

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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/file"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secretscoping))
}

type secretscoping struct {
	daprd   *procdaprd.Daprd
	logline *logline.LogLine

	secretStoreNames []string
}

func (c *secretscoping) Setup(t *testing.T) []framework.Option {
	c.secretStoreNames = []string{"secretstore1", "secretstore2"}
	secretFileNames := file.Paths(t, len(c.secretStoreNames))
	files := make([]string, len(c.secretStoreNames))
	for i, secretFileName := range secretFileNames {
		require.NoError(t, os.WriteFile(secretFileName, []byte("{}"), 0o600))

		files[i] = fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: '%s'
spec:
  type: secretstores.local.file
  version: v1
  metadata:
  - name: secretsFile
    value: '%s'
`,
			// Escape single quotes in the store name.
			strings.ReplaceAll(c.secretStoreNames[i], "'", "''"),
			strings.ReplaceAll(secretFileName, "'", "''"),
		)
	}

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: myconfig
spec:
  secrets:
    scopes:
    - storeName: customsecretstore1
      defaultAccess: deny
      deniedSecrets: ["secret-name"]
    - storeName: secretstore1
      defaultAccess: allow
    - storeName: secretstore2
      defaultAccess: deny
`), 0o600))

	c.logline = logline.New(t,
		logline.WithStdoutLineContains(
			"Secrets configuration added for 'customsecretstore1', but no matching secret store was found",
		),
	)

	c.daprd = procdaprd.New(t,
		procdaprd.WithResourceFiles(files...),
		procdaprd.WithConfigs(configFile),
		procdaprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(c.daprd),
	}
}

func (c *secretscoping) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := client.HTTP(t)
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/customsecretstore1/key1", c.daprd.HTTPPort())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Contains(t, string(respBody), "ERR_SECRET_STORE_NOT_FOUND")
	assert.Contains(t, string(respBody), "failed finding secret store with key customsecretstore1")
}
