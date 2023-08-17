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

package hotreloading

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpoint))
}

type httpendpoint struct {
	daprd  *procdaprd.Daprd
	client *http.Client

	resDir1 string
	resDir2 string
	resDir3 string

	srv1 *prochttp.HTTP
	srv2 *prochttp.HTTP
	srv3 *prochttp.HTTP
}

func (h *httpendpoint) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func(route string) *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			var resp []string
			for k, v := range r.Header {
				if k == "User-Agent" || k == "Accept-Encoding" || k == "Traceparent" {
					continue
				}
				resp = append(resp, fmt.Sprintf("%s=%s", k, v))
			}
			sort.Strings(resp)
			w.Write([]byte(strings.Join(resp, "\n")))
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
    - name: HotReload
      enabled: true`), 0o600))

	h.resDir1, h.resDir2, h.resDir3 = t.TempDir(), t.TempDir(), t.TempDir()
	h.client = util.HTTPClient(t)

	h.srv1, h.srv2, h.srv3 = newHTTPServer("/route1"), newHTTPServer("/route2"), newHTTPServer("/route3")

	h.daprd = procdaprd.New(t,
		procdaprd.WithConfigs(configFile),
		procdaprd.WithResourcesDir(h.resDir1, h.resDir2, h.resDir3),
	)

	return []framework.Option{
		framework.WithProcesses(h.daprd, h.srv1, h.srv2, h.srv3),
	}
}

func (h *httpendpoint) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	t.Run("expect no endpoints to be loaded yet", func(t *testing.T) {
		assert.Len(t, getMetaHTTPEndpoints(t, ctx, h.client, h.daprd.HTTPPort()), 0)
		h.doReq(t, ctx, "abc1", "route1",
			`\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc1, err: couldn't find service: abc1"\}`,
			500,
		)

		require.NoError(t, os.WriteFile(filepath.Join(h.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc1
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: foo
    value: bar
`, h.srv1.Port())), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{
				{Name: "abc1"},
			})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc1", "route1", `Foo=\[bar\]`, 200)
	})

	t.Run("adding a second and third component should also become available", func(t *testing.T) {
		h.doReq(t, ctx, "abc2", "route2",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc2, err:`,
			500,
		)
		h.doReq(t, ctx, "abc3", "route3",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc3, err:`,
			500,
		)

		require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc2
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: baz
    value: boo
`, h.srv2.Port())), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir3, "3.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc3
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: xyz
    value: xyz
`, h.srv3.Port())), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{
				{Name: "abc1"}, {Name: "abc2"}, {Name: "abc3"},
			})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc1", "route1", `Foo=\[bar\]`, 200)
		h.doReq(t, ctx, "abc2", "route2", `Baz=\[boo\]`, 200)
		h.doReq(t, ctx, "abc3", "route3", `Xyz=\[xyz\]`, 200)
	})

	t.Run("changing the headers on an endpoint should restart and still be available", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc1
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: foo
    value: bar
  - name: bar
    value: foo
`, h.srv1.Port())), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			h.doReq(c, ctx, "abc1", "route1", "Bar=\\[foo\\]\nFoo=\\[bar\\]", 200)
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc1", "route1", "Bar=\\[foo\\]\nFoo=\\[bar\\]", 200)
		h.doReq(t, ctx, "abc2", "route2", `Baz=\[boo\]`, 200)
		h.doReq(t, ctx, "abc3", "route3", `Xyz=\[xyz\]`, 200)
	})

	t.Run("updating multiple endpoints should be updated, and multiple endpoints in a single file", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir2, "2.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc2
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: baz
    value: boo
  - name: querty
    value: keyboard
---
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: 1234
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: dapr
    value: helloworld
`, h.srv2.Port(), h.srv3.Port())), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir3, "3.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: abc3
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: zyx
    value: zyx
`, h.srv3.Port())), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{
				{Name: "abc1"}, {Name: "abc2"}, {Name: "abc3"}, {Name: "1234"},
			})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc1", "route1", "Bar=\\[foo\\]\nFoo=\\[bar\\]", 200)
		h.doReq(t, ctx, "abc2", "route2", `Baz=\[boo\]`, 200)
		h.doReq(t, ctx, "abc3", "route3", `Zyx=\[zyx\]`, 200)
		h.doReq(t, ctx, "1234", "route3", `Dapr=\[helloworld\]`, 200)
	})

	t.Run("renaming an endpoint should close the old name, and open the new one", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir1, "1.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: def1
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
  headers:
  - name: foo
    value: bar
`, h.srv1.Port())), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{
				{Name: "def1"}, {Name: "abc2"}, {Name: "abc3"}, {Name: "1234"},
			})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc1", "route1",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc1, err:`,
			500,
		)
		h.doReq(t, ctx, "def1", "route1", `Foo=\[bar\]`, 200)
		h.doReq(t, ctx, "abc2", "route2", `Baz=\[boo\]`, 200)
		h.doReq(t, ctx, "abc3", "route3", `Zyx=\[zyx\]`, 200)
		h.doReq(t, ctx, "1234", "route3", `Dapr=\[helloworld\]`, 200)
	})

	t.Run("deleting an endpoint file should delete the endpoints", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir2, "2.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{
				{Name: "def1"}, {Name: "abc3"},
			})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "abc2", "route1",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc2, err:`,
			500,
		)
		h.doReq(t, ctx, "1234", "route1",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: 1234, err:`,
			500,
		)
		h.doReq(t, ctx, "def1", "route1", `Foo=\[bar\]`, 200)
		h.doReq(t, ctx, "abc3", "route3", `Zyx=\[zyx\]`, 200)
	})

	t.Run("deleting all endpoints should result in no endpoints remaining", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir1, "1.yaml")))
		require.NoError(t, os.Remove(filepath.Join(h.resDir3, "3.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp := getMetaHTTPEndpoints(c, ctx, h.client, h.daprd.HTTPPort())
			assert.ElementsMatch(c, resp, []*runtimev1pb.MetadataHTTPEndpoint{})
		}, time.Second*5, time.Millisecond*100)

		h.doReq(t, ctx, "def1", "route1",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: def1, err:`,
			500,
		)
		h.doReq(t, ctx, "abc3", "route3",
			`^\{"errorCode":"ERR_DIRECT_INVOKE","message":"failed to invoke, id: abc3, err:`,
			500,
		)
	})

}

func (h *httpendpoint) doReq(t require.TestingT, ctx context.Context, name, route, expBody string, expCode int) {
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", h.daprd.HTTPPort(), name, route)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	require.NoError(t, err)
	resp, err := h.client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, expCode, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Regexp(t, expBody, string(body))
}
