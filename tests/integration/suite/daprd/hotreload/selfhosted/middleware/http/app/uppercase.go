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
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	daprd3 *daprd.Daprd

	resDir1 string
	resDir2 string
	resDir3 string
}

func (u *uppercase) Setup(t *testing.T) []framework.Option {
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
  appHttpPipeline:
    handlers:
    - name: uppercase
      type: middleware.http.uppercase
    - name: uppercase2
      type: middleware.http.uppercase
`), 0o600))

	u.resDir1, u.resDir2, u.resDir3 = t.TempDir(), t.TempDir(), t.TempDir()

	handler := nethttp.NewServeMux()
	handler.HandleFunc("/", func(nethttp.ResponseWriter, *nethttp.Request) {})
	handler.HandleFunc("/foo", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, err := io.Copy(w, r.Body)
		require.NoError(t, err)
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	require.NoError(t, os.WriteFile(filepath.Join(u.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'uppercase'
spec:
  type: middleware.http.uppercase
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'uppercase2'
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: "routes"
   value: '{"/foo":"/v1.0/invoke/nowhere/method/bar"}'
`), 0o600))

	u.daprd1 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(u.resDir1),
		daprd.WithAppPort(srv.Port()),
	)
	u.daprd2 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(u.resDir2),
		daprd.WithAppPort(srv.Port()),
	)
	u.daprd3 = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(u.resDir3),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, u.daprd1, u.daprd2, u.daprd3),
	}
}

func (u *uppercase) Run(t *testing.T, ctx context.Context) {
	u.daprd1.WaitUntilAppHealth(t, ctx)
	u.daprd2.WaitUntilAppHealth(t, ctx)
	u.daprd3.WaitUntilAppHealth(t, ctx)

	client := util.HTTPClient(t)
	assert.Len(t, util.GetMetaComponents(t, ctx, client, u.daprd1.HTTPPort()), 2)

	t.Run("existing middleware should be loaded", func(t *testing.T) {
		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})

	t.Run("adding a new middleware should be loaded", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(u.resDir2, "2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'uppercase'
spec:
 type: middleware.http.uppercase
 version: v1
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(t, ctx, client, u.daprd2.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})

	t.Run("adding third middleware should be loaded", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(u.resDir3, "3.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'uppercase'
spec:
 type: middleware.http.uppercase
 version: v1
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, util.GetMetaComponents(t, ctx, client, u.daprd3.HTTPPort()), 1)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("changing the type of middleware should no longer make it available as type needs to match", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(u.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'uppercase'
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: "routes"
   value: '{"/foo":"/v1.0/invoke/nowhere/method/bar"}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'uppercase2'
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: "routes"
   value: '{"/foo":"/v1.0/invoke/nowhere/method/bar"}'
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, u.daprd1.HTTPPort())
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "uppercase", Type: "middleware.http.routeralias", Version: "v1"},
				{Name: "uppercase2", Type: "middleware.http.routeralias", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("changing the type of middleware should make it available as type needs to match", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(u.resDir1, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'uppercase'
spec:
 type: middleware.http.routeralias
 version: v1
 metadata:
 - name: "routes"
   value: '{"/foo":"/v1.0/invoke/nowhere/method/bar"}'
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'uppercase2'
spec:
 type: middleware.http.uppercase
 version: v1
`), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := util.GetMetaComponents(c, ctx, client, u.daprd1.HTTPPort())
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "uppercase", Type: "middleware.http.routeralias", Version: "v1"},
				{Name: "uppercase2", Type: "middleware.http.uppercase", Version: "v1"},
			}, resp)
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, true)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, true)
	})

	t.Run("deleting components should no longer make them available", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(u.resDir1, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(u.resDir2, "2.yaml")))
		require.NoError(t, os.Remove(filepath.Join(u.resDir3, "3.yaml")))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, u.daprd1.HTTPPort()))
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, u.daprd2.HTTPPort()))
			assert.Empty(c, util.GetMetaComponents(c, ctx, client, u.daprd3.HTTPPort()))
		}, time.Second*5, time.Millisecond*100, "expected component to be loaded")

		u.doReq(t, ctx, client, u.daprd1, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd1, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd2, u.daprd3, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd1, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd2, false)
		u.doReq(t, ctx, client, u.daprd3, u.daprd3, false)
	})
}

func (u *uppercase) doReq(t require.TestingT, ctx context.Context, client *nethttp.Client, source, target *daprd.Daprd, expectUpper bool) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", source.HTTPPort(), target.AppID())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, strings.NewReader("hello"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if expectUpper {
		assert.Equal(t, "HELLO", string(body))
	} else {
		assert.Equal(t, "hello", string(body))
	}
}
