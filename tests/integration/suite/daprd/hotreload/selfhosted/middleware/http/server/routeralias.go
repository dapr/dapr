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

package server

import (
	"context"
	"fmt"
	"io"
	nethttp "net/http"
	"os"
	"path/filepath"
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
	suite.Register(new(routeralias))
}

type routeralias struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd

	resDir string
}

func (r *routeralias) Setup(t *testing.T) []framework.Option {
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
    - name: routeralias1
      type: middleware.http.routeralias
    - name: routeralias2
      type: middleware.http.routeralias
    - name: routeralias3
      type: middleware.http.routeralias
`), 0o600))

	r.resDir = t.TempDir()

	srv := func(id string) *prochttp.HTTP {
		handler := nethttp.NewServeMux()
		handler.HandleFunc("/", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			fmt.Fprintf(w, "%s:%s", id, r.URL.Path)
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := srv("daprd1")
	srv2 := srv("daprd2")

	r.daprd1 = daprd.New(t,
		daprd.WithAppPort(srv1.Port()),
	)
	r.daprd2 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resDir),
		daprd.WithAppPort(srv2.Port()),
	)

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "res.yaml"), []byte(
		fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: routeralias1
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: routes
   value: '{ "/helloworld": "/v1.0/invoke/%[1]s/method/foobar" }'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: routeralias2
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: routes
   value: '{
	  "/helloworld": "/v1.0/invoke/%[1]s/method/barfoo",
		"/v1.0/invoke/%[1]s/method/foobar": "/v1.0/invoke/%[1]s/method/abc"
	}'
`, r.daprd1.AppID()),
	), 0o600))

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, r.daprd1, r.daprd2),
	}
}

func (r *routeralias) Run(t *testing.T, ctx context.Context) {
	r.daprd1.WaitUntilAppHealth(t, ctx)
	r.daprd2.WaitUntilAppHealth(t, ctx)

	client := util.HTTPClient(t)
	r.doReq(t, ctx, client, "helloworld", "daprd1:/abc")

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "res.yaml"), []byte(
		fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: routeralias1
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: routes
   value: '{
		"/v1.0/invoke/%[1]s/method/barfoo": "/v1.0/invoke/%[1]s/method/aaa"
	}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: routeralias2
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: routes
   value: '{
	  "/helloworld": "/v1.0/invoke/%[1]s/method/barfoo",
		"/v1.0/invoke/%[1]s/method/abc": "/v1.0/invoke/%[1]s/method/aaa"
	}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: routeralias3
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: routes
   value: '{
		"/v1.0/invoke/%[1]s/method/barfoo": "/v1.0/invoke/%[1]s/method/xyz"
	}'
`, r.daprd1.AppID()),
	), 0o600))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		r.doReq(c, ctx, client, "helloworld", "daprd1:/xyz")
	}, time.Second*10, time.Millisecond*100)
}

func (r *routeralias) doReq(t require.TestingT, ctx context.Context, client *nethttp.Client, path, expect string) {
	url := fmt.Sprintf("http://localhost:%d/%s", r.daprd2.HTTPPort(), path)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, expect, string(body))
}
