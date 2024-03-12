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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	newHTTPServer := func() *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPatch:
				w.WriteHeader(http.StatusBadGateway)
			case http.MethodPost:
				w.WriteHeader(http.StatusCreated)
			case http.MethodGet:
				w.WriteHeader(http.StatusOK)
			case http.MethodPut:
				w.WriteHeader(http.StatusAccepted)
			case http.MethodDelete:
				w.WriteHeader(http.StatusConflict)
			}
			w.Write([]byte(r.Method))
		})

		handler.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("x-method", r.Method)
			io.Copy(w, r.Body)
		})

		handler.HandleFunc("/with-headers-and-body", func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if string(body) != "hello" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Header.Get("foo") != "bar" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		handler.HandleFunc("/multiple/segments", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/multiple/segments" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	srv1 := newHTTPServer()
	srv2 := newHTTPServer()
	b.daprd1 = procdaprd.New(t, procdaprd.WithAppPort(srv1.Port()))
	b.daprd2 = procdaprd.New(t, procdaprd.WithAppPort(srv2.Port()))

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, b.daprd1, b.daprd2),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd1.WaitUntilRunning(t, ctx)
	b.daprd2.WaitUntilRunning(t, ctx)

	httpClient := util.HTTPClient(t)

	t.Run("invoke url", func(t *testing.T) {
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

		for _, ts := range []struct {
			url     string
			headers map[string]string
		}{
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", b.daprd1.HTTPPort(), b.daprd2.AppID())},
			{url: fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", b.daprd2.HTTPPort(), b.daprd1.AppID())},
			{url: fmt.Sprintf("http://localhost:%d/v1.0////invoke/%s/method/foo", b.daprd2.HTTPPort(), b.daprd1.AppID())},
			{url: fmt.Sprintf("http://localhost:%d/v1.0//invoke//%s/method//foo", b.daprd1.HTTPPort(), b.daprd2.AppID())},
			{url: fmt.Sprintf("http://localhost:%d///foo", b.daprd1.HTTPPort()), headers: map[string]string{
				"foo":         "bar",
				"dapr-app-id": b.daprd2.AppID(),
			}},
		} {
			status, body := doReq(http.MethodGet, ts.url, ts.headers)
			assert.Equal(t, http.StatusOK, status)
			assert.Equal(t, "GET", body)

			status, body = doReq(http.MethodPost, ts.url, ts.headers)
			assert.Equal(t, http.StatusCreated, status)
			assert.Equal(t, "POST", body)

			status, body = doReq(http.MethodPut, ts.url, ts.headers)
			assert.Equal(t, http.StatusAccepted, status)
			assert.Equal(t, "PUT", body)

			status, body = doReq(http.MethodDelete, ts.url, ts.headers)
			assert.Equal(t, http.StatusConflict, status)
			assert.Equal(t, "DELETE", body)

			status, body = doReq(http.MethodPatch, ts.url, ts.headers)
			assert.Equal(t, http.StatusBadGateway, status)
			assert.Equal(t, "PATCH", body)
		}
	})

	t.Run("invoke url with body and headers", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/with-headers-and-body", b.daprd1.HTTPPort(), b.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader("hello"))
		require.NoError(t, err)
		req.Header.Set("foo", "bar")
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	t.Run("method doesn't exist", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/doesntexist", b.daprd1.HTTPPort(), b.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	t.Run("no method", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s", b.daprd1.HTTPPort(), b.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		reqURL = fmt.Sprintf("http://localhost:%d/", b.daprd1.HTTPPort())
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		req.Header.Set("dapr-app-id", b.daprd2.AppID())
		resp, err = util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	})

	t.Run("multiple segments", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/multiple/segments", b.daprd1.HTTPPort(), b.daprd2.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, "ok", string(body))
	})

	client := util.HTTPClient(t)
	pt := util.NewParallel(t)
	for i := 0; i < 100; i++ {
		pt.Add(func(t *assert.CollectT) {
			u := uuid.New().String()
			reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/echo", b.daprd1.HTTPPort(), b.daprd2.AppID())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(u))
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, "POST", resp.Header.Get("x-method"))
			assert.Equal(t, u, string(body))
		})
	}
}
