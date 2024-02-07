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

package app

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
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(uppercase))
}

type uppercase struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (u *uppercase) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: uppercase
spec:
  appHttpPipeline:
    handlers:
      - name: uppercase
        type: middleware.http.uppercase
`), 0o600))

	handler := http.NewServeMux()
	handler.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	handler.HandleFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(w, r.Body)
		require.NoError(t, err)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	u.daprd1 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: uppercase
spec:
  type: middleware.http.uppercase
  version: v1
`),
		daprd.WithAppPort(srv.Port()),
	)
	u.daprd2 = daprd.New(t, daprd.WithAppPort(srv.Port()))

	return []framework.Option{
		framework.WithProcesses(srv, u.daprd1, u.daprd2),
	}
}

func (u *uppercase) Run(t *testing.T, ctx context.Context) {
	u.daprd1.WaitUntilRunning(t, ctx)
	u.daprd2.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", u.daprd1.HTTPPort(), u.daprd2.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("hello"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "hello", string(body))

	url = fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", u.daprd1.HTTPPort(), u.daprd1.AppID())
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("hello"))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "HELLO", string(body))

	url = fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", u.daprd2.HTTPPort(), u.daprd1.AppID())
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("hello"))
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "HELLO", string(body))
}
