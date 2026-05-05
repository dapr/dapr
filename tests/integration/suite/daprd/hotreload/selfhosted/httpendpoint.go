/*
Copyright 2026 The Dapr Authors
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

package selfhosted

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpendpoint))
}

// httpendpoint tests that HTTP endpoint resources are hot-reloaded via file
// watcher in selfhosted mode when HotReload feature is enabled, and that the
// endpoints are actually invocable through the Dapr service invocation API.
type httpendpoint struct {
	daprd   *daprd.Daprd
	backend *prochttp.HTTP
	resDir  string
}

func (h *httpendpoint) Setup(t *testing.T) []framework.Option {
	h.resDir = t.TempDir()

	h.backend = prochttp.New(t,
		prochttp.WithHandlerFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "hello-response")
		}),
		prochttp.WithHandlerFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			body, _ := io.ReadAll(r.Body)
			w.Write(body)
		}),
	)

	h.daprd = daprd.New(t,
		daprd.WithResourcesDir(h.resDir),
	)

	return []framework.Option{
		framework.WithProcesses(h.backend, h.daprd),
	}
}

func (h *httpendpoint) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	t.Run("no HTTP endpoints initially", func(t *testing.T) {
		assert.Empty(t, h.daprd.GetMetaHTTPEndpoints(t, ctx))
	})

	t.Run("invoking non-existent endpoint returns error", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/myendpoint/method/hello", h.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("adding HTTP endpoint file triggers reload and endpoint becomes invocable", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "endpoint.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: myendpoint
spec:
  baseUrl: http://localhost:%d
`, h.backend.Port()), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(c, ctx)
			assert.Len(c, endpoints, 1)
		}, 20*time.Second, 10*time.Millisecond)

		assert.ElementsMatch(t, []*rtv1.MetadataHTTPEndpoint{
			{Name: "myendpoint"},
		}, h.daprd.GetMetaHTTPEndpoints(t, ctx))

		// Invoke the endpoint and verify we get the backend response.
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/myendpoint/method/hello", h.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "hello-response", string(body))
	})

	t.Run("removing HTTP endpoint file triggers reload and endpoint is no longer invocable", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(h.resDir, "endpoint.yaml")))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(c, ctx)
			assert.Empty(c, endpoints)
		}, 20*time.Second, 10*time.Millisecond)

		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/myendpoint/method/hello", h.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("re-adding HTTP endpoint makes it invocable again", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(h.resDir, "endpoint.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: myendpoint
spec:
  baseUrl: http://localhost:%d
`, h.backend.Port()), 0o600))

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			endpoints := h.daprd.GetMetaHTTPEndpoints(c, ctx)
			assert.Len(c, endpoints, 1)
		}, 20*time.Second, 10*time.Millisecond)

		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/myendpoint/method/echo", h.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("ping"))
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(body))
	})
}
