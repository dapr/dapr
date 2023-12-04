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

package selfhosted

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(middleware))
}

type middleware struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd

	resDir string
	respCh chan int
}

func (m *middleware) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true
  httpPipeline:
    handlers:
    - name: routeralias
      type: middleware.http.routeralias
`), 0o600))

	m.resDir = t.TempDir()

	m.respCh = make(chan int, 1)
	newHTTPServer := func(i int) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		})
		handler.HandleFunc("/helloworld", func(w http.ResponseWriter, r *http.Request) {
			m.respCh <- i
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'routeralias'
spec:
  type: middleware.http.routeralias
  version: v1
  metadata:
    - name: "routes"
      value: '{}'
`), 0o600))

	srv1 := newHTTPServer(0)
	srv2 := newHTTPServer(1)
	srv3 := newHTTPServer(2)

	m.daprd1 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(m.resDir),
		daprd.WithAppPort(srv1.Port()),
		daprd.WithAppID("app1"),
	)
	m.daprd2 = daprd.New(t, daprd.WithAppPort(srv2.Port()), daprd.WithAppID("app2"))
	m.daprd3 = daprd.New(t, daprd.WithAppPort(srv3.Port()), daprd.WithAppID("app3"))

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, srv3, m.daprd1, m.daprd2, m.daprd3),
	}
}

func (m *middleware) Run(t *testing.T, ctx context.Context) {
	m.daprd1.WaitUntilRunning(t, ctx)
	m.daprd2.WaitUntilRunning(t, ctx)
	m.daprd3.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)

	m.doReq(t, ctx, client, "/v1.0/invoke/app1/method/helloworld", http.StatusOK)
	m.expServerResp(t, ctx, 0)
	m.doReq(t, ctx, client, "/v1.0/invoke/app2/method/helloworld", http.StatusOK)
	m.expServerResp(t, ctx, 1)
	m.doReq(t, ctx, client, "/v1.0/invoke/app3/method/helloworld", http.StatusOK)
	m.expServerResp(t, ctx, 2)

	ctx, cancel := context.WithTimeout(ctx, time.Second*5)
	t.Cleanup(cancel)

	t.Run("expect middleware to be loaded", func(t *testing.T) {
		assert.Len(t, util.GetMetaComponents(t, ctx, client, m.daprd1.HTTPPort()), 1)
	})

	t.Run("middleware hot reloading doesn't work yet", func(t *testing.T) {
		m.doReq(t, ctx, client, "/v1.0/invoke/app1/method/helloworld", http.StatusOK)
		m.expServerResp(t, ctx, 0)
		m.doReq(t, ctx, client, "/helloworld", http.StatusNotFound)

		require.NoError(t, os.WriteFile(filepath.Join(m.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'routeralias'
spec:
  type: middleware.http.routeralias
  version: v1
  metadata:
    - name: "routes"
      value: '{"/helloworld2":"/v1.0/invoke/app2/method/helloworld"}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'state1'
spec:
  type: state.in-memory
  version: v1
`), 0o600))
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(t, ctx, client, m.daprd1.HTTPPort()), 2)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")
		m.doReq(t, ctx, client, "/helloworld", http.StatusNotFound)
	})
}

func (m *middleware) doReq(t require.TestingT, ctx context.Context, client *http.Client, path string, expCode int) {
	reqURL := "http://localhost:" + strconv.Itoa(m.daprd1.HTTPPort()) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, expCode, resp.StatusCode, path)
}

func (m *middleware) expServerResp(t *testing.T, ctx context.Context, server int) {
	select {
	case <-ctx.Done():
		t.Fatal("timed out waiting for response")
	case got := <-m.respCh:
		assert.Equal(t, server, got, "unexpected server response")
	}
}
