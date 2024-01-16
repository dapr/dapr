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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	testsutil "github.com/dapr/dapr/tests/util"
)

func init() {
	suite.Register(new(httpendpoints))
}

type httpendpoints struct {
	daprd1  *procdaprd.Daprd
	daprd2  *procdaprd.Daprd
	appPort int
}

func (h *httpendpoints) Setup(t *testing.T) []framework.Option {
	pki1 := testsutil.GenPKI(t, testsutil.PKIOptions{LeafDNS: "localhost"})
	pki2 := testsutil.GenPKI(t, testsutil.PKIOptions{LeafDNS: "localhost"})

	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	newHTTPServerTLS := func() *prochttp.HTTP {
		handler := http.NewServeMux()

		handler.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok-TLS"))
		})

		return prochttp.New(t, prochttp.WithHandler(handler), prochttp.WithMTLS(t, pki1.RootCertPEM, pki1.LeafCertPEM, pki1.LeafPKPEM))
	}

	srv1 := newHTTPServer()
	srv2 := newHTTPServerTLS()

	h.daprd1 = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsite
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
`, srv1.Port()), fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsitetls
spec:
  version: v1alpha1
  baseUrl: https://localhost:%d
  clientTLS:
    rootCA:
      value: "%s"
    certificate:
      value: "%s"
    privateKey:
      value: "%s"
`, srv2.Port(),
		strings.ReplaceAll(string(pki1.RootCertPEM), "\n", "\\n"),
		strings.ReplaceAll(string(pki1.LeafCertPEM), "\n", "\\n"),
		strings.ReplaceAll(string(pki1.LeafPKPEM), "\n", "\\n"))))

	h.daprd2 = procdaprd.New(t, procdaprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsite
spec:
  version: v1alpha1
  baseUrl: http://localhost:%d
`, srv1.Port()), fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: HTTPEndpoint
metadata:
  name: mywebsitetls
spec:
  version: v1alpha1
  baseUrl: https://localhost:%d
  clientTLS:
    rootCA:
      value: "%s"
    certificate:
      value: "%s"
    privateKey:
      value: "%s"
`, srv2.Port(),
		strings.ReplaceAll(string(pki1.RootCertPEM), "\n", "\\n"),
		strings.ReplaceAll(string(pki2.LeafCertPEM), "\n", "\\n"),
		strings.ReplaceAll(string(pki2.LeafPKPEM), "\n", "\\n"))))
	h.appPort = srv1.Port()

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, h.daprd1, h.daprd2),
	}
}

func (h *httpendpoints) Run(t *testing.T, ctx context.Context) {
	h.daprd1.WaitUntilRunning(t, ctx)
	h.daprd2.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	invokeTests := func(t *testing.T, expTLSCode int, assertBody func(t *testing.T, body string), daprd *procdaprd.Daprd) {
		for _, port := range []int{
			h.daprd1.HTTPPort(),
			h.daprd2.HTTPPort(),
		} {
			assert.EventuallyWithT(t, func(t *assert.CollectT) {
				url := fmt.Sprintf("http://localhost:%d/v1.0/metadata", port)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				require.NoError(t, err)

				resp, err := httpClient.Do(req)
				require.NoError(t, err)

				body := make(map[string]any)
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
				require.NoError(t, resp.Body.Close())
				endpoints, ok := body["httpEndpoints"]
				_ = assert.True(t, ok) && assert.Len(t, endpoints.([]any), 2)
			}, time.Second*5, time.Millisecond*100)
		}

		t.Run("invoke http endpoint", func(t *testing.T) {
			doReq := func(method, url string, headers map[string]string) (int, string) {
				req, err := http.NewRequestWithContext(ctx, method, url, nil)
				require.NoError(t, err)
				for k, v := range headers {
					req.Header.Set(k, v)
				}
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				return resp.StatusCode, string(body)
			}

			for i, ts := range []struct {
				url     string
				headers map[string]string
			}{
				{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/http://localhost:%d/method/hello", daprd.HTTPPort(), h.appPort)},
				{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/mywebsite/method/hello", daprd.HTTPPort())},
			} {
				t.Run(fmt.Sprintf("url %d", i), func(t *testing.T) {
					status, body := doReq(http.MethodGet, ts.url, ts.headers)
					assert.Equal(t, http.StatusOK, status)
					assert.Equal(t, "ok", body)
				})
			}
		})

		t.Run("invoke TLS http endpoint", func(t *testing.T) {
			doReq := func(method, url string, headers map[string]string) (int, string) {
				req, err := http.NewRequestWithContext(ctx, method, url, nil)
				require.NoError(t, err)
				for k, v := range headers {
					req.Header.Set(k, v)
				}
				resp, err := httpClient.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				return resp.StatusCode, string(body)
			}

			for i, ts := range []struct {
				url     string
				headers map[string]string
			}{
				{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/mywebsitetls/method/hello", daprd.HTTPPort())},
			} {
				t.Run(fmt.Sprintf("url %d", i), func(t *testing.T) {
					for {
						status, body := doReq(http.MethodGet, ts.url, ts.headers)
						assert.Equal(t, expTLSCode, status)
						if runtime.GOOS == "windows" &&
							strings.Contains(body, "An existing connection was forcibly closed by the remote host.") {
							t.Logf("retrying due to: %s", body)
							select {
							case <-ctx.Done():
								assert.Fail(t, "context done")
							case <-time.After(time.Millisecond * 100):
								continue
							}
						}
						assertBody(t, body)
						break
					}
				})
			}
		})
	}

	t.Run("good PKI", func(t *testing.T) {
		invokeTests(t, http.StatusOK, func(t *testing.T, body string) {
			assert.Equal(t, "ok-TLS", body)
		}, h.daprd1)
	})

	t.Run("bad PKI", func(t *testing.T) {
		invokeTests(t, http.StatusInternalServerError, func(t *testing.T, body string) {
			assert.Contains(t, body, `"errorCode":"ERR_DIRECT_INVOKE"`)
			assert.Contains(t, body, "tls: unknown certificate authority")
		}, h.daprd2)
	})
}
