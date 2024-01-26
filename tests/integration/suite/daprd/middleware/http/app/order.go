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
	suite.Register(new(order))
}

type order struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (o *order) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: order
spec:
  appHttpPipeline:
    handlers:
    - name: routeralias1
      type: middleware.http.routeralias
    - name: routeralias2
      type: middleware.http.routeralias
`), 0o600))

	srv := func(id string) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s:%s", id, r.URL.Path)
		})
		return prochttp.New(t, prochttp.WithHandler(handler))
	}
	srv1 := srv("daprd1")
	srv2 := srv("daprd2")

	o.daprd1 = daprd.New(t,
		daprd.WithAppPort(srv1.Port()),
	)
	o.daprd2 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourceFiles(`
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
	  "/helloworld": "/foobar",
	  "/xyz": "/abc"
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
	  "/helloworld": "/xyz",
		"/foobar": "/abc"
	}'
`),
		daprd.WithAppPort(srv2.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, o.daprd1, o.daprd2),
	}
}

func (o *order) Run(t *testing.T, ctx context.Context) {
	o.daprd1.WaitUntilRunning(t, ctx)
	o.daprd2.WaitUntilRunning(t, ctx)

	client := util.HTTPClient(t)
	o.doReq(t, ctx, client, fmt.Sprintf("/v1.0/invoke/%s/method/helloworld", o.daprd2.AppID()), "daprd2:/abc")
}

func (o *order) doReq(t require.TestingT, ctx context.Context, client *http.Client, path, expect string) {
	url := fmt.Sprintf("http://localhost:%d/%s", o.daprd1.HTTPPort(), path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, expect, string(body))
}
