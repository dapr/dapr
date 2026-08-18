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

package reservedchars

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pathcorrectness))
}

type pathcorrectness struct {
	caller *procdaprd.Daprd
	callee *procdaprd.Daprd
}

func (r *pathcorrectness) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		w.Write([]byte(req.URL.Path + "|" + req.URL.RawQuery))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	r.callee = procdaprd.New(t, procdaprd.WithAppPort(srv.Port()))
	r.caller = procdaprd.New(t)

	return []framework.Option{
		framework.WithProcesses(srv, r.callee, r.caller),
	}
}

func (r *pathcorrectness) Run(t *testing.T, ctx context.Context) {
	r.caller.WaitUntilRunning(t, ctx)
	r.callee.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	invoke := func(t *testing.T, methodSuffix string) (int, string) {
		t.Helper()
		reqURL := fmt.Sprintf(
			"http://localhost:%d/v1.0/invoke/%s/method/%s",
			r.caller.HTTPPort(),
			r.callee.AppID(),
			methodSuffix,
		)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	// Forbidden characters: encoded #, ?, % are rejected after decoding.
	t.Run("encoded hash is rejected", func(t *testing.T) {
		status, _ := invoke(t, "test%23stream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("encoded question mark is rejected", func(t *testing.T) {
		status, _ := invoke(t, "test%3Fstream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("encoded percent is allowed", func(t *testing.T) {
		// HTTP decodes %25 → %, NormalizeMethod allows it, but
		// constructRequest fails building the outbound URL because
		// Go's url.Parse rejects bare % followed by non-hex chars.
		status, _ := invoke(t, "test%25stream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("encoded null byte is rejected", func(t *testing.T) {
		status, _ := invoke(t, "test%00stream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("encoded carriage return is rejected", func(t *testing.T) {
		status, _ := invoke(t, "test%0Dstream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("multiple forbidden characters rejected", func(t *testing.T) {
		status, _ := invoke(t, "a%23b%3Fc%25d")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	// Safe characters: decoded and passed through to callee.
	t.Run("double quote in path", func(t *testing.T) {
		status, body := invoke(t, "test%22stream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, `/test"stream|`, body)
	})

	t.Run("asterisk in path", func(t *testing.T) {
		status, body := invoke(t, "test%2Astream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/test*stream|", body)
	})

	t.Run("backslash in path", func(t *testing.T) {
		status, body := invoke(t, "test%5Cstream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, `/test\stream|`, body)
	})

	// Traversal: resolved by path.Clean, callee gets the clean path.
	t.Run("encoded traversal is resolved", func(t *testing.T) {
		status, body := invoke(t, "admin%2F..%2Fpublic")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/public|", body)
	})

	// Literal characters: HTTP client handles these before they reach Dapr.
	t.Run("literal hash truncated by HTTP client", func(t *testing.T) {
		status, body := invoke(t, "test#stream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/test|", body)
	})

	t.Run("literal question mark split by HTTP client", func(t *testing.T) {
		status, body := invoke(t, "test?stream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/test|stream", body)
	})

	t.Run("literal quote in path", func(t *testing.T) {
		status, body := invoke(t, `test"stream`)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, `/test"stream|`, body)
	})

	t.Run("literal asterisk in path", func(t *testing.T) {
		status, body := invoke(t, "test*stream")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/test*stream|", body)
	})

	t.Run("literal backslash in path", func(t *testing.T) {
		status, body := invoke(t, `test\stream`)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, `/test\stream|`, body)
	})

	// dapr-app-id header path: the method is the entire URL path.
	// Tests the other branch in findTargetIDAndMethod.
	invokeViaHeader := func(t *testing.T, path string) (int, string) {
		t.Helper()
		reqURL := fmt.Sprintf("http://localhost:%d%s", r.caller.HTTPPort(), path)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		req.Header.Set("dapr-app-id", r.callee.AppID())
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	t.Run("header: normal path", func(t *testing.T) {
		status, body := invokeViaHeader(t, "/mymethod")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/mymethod|", body)
	})

	t.Run("header: traversal resolved", func(t *testing.T) {
		status, body := invokeViaHeader(t, "/admin/../public")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/public|", body)
	})

	t.Run("header: encoded hash rejected", func(t *testing.T) {
		status, _ := invokeViaHeader(t, "/test%23stream")
		assert.Equal(t, http.StatusInternalServerError, status)
	})

	t.Run("header: encoded traversal resolved", func(t *testing.T) {
		status, body := invokeViaHeader(t, "/admin%2F..%2Fpublic")
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, "/public|", body)
	})
}
