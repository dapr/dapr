/*
Copyright 2023 The Dapr Authors
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

package httpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(httpServer))
}

// httpServer tests Dapr's HTTP server features.
type httpServer struct {
	proc *procdaprd.Daprd
}

func (h *httpServer) Setup(t *testing.T) []framework.Option {
	h.proc = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(h.proc),
	}
}

func (h *httpServer) Run(t *testing.T, ctx context.Context) {
	h.proc.WaitUntilRunning(t, ctx)

	h1Client := util.HTTPClient(t)
	h2cClient := &http.Client{
		Transport: &http2.Transport{
			// Allow http2.Transport to use protocol "http"
			AllowHTTP: true,
			// Pretend we are dialing a TLS endpoint
			DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
	t.Cleanup(h2cClient.CloseIdleConnections)

	t.Run("test with HTTP1", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer reqCancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/healthz", h.proc.HTTPPort()), nil)
		require.NoError(t, err)

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		assert.Equal(t, 1, res.ProtoMajor)
	})

	t.Run("test with HTTP2 Cleartext with prior knowledge", func(t *testing.T) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer reqCancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/healthz", h.proc.HTTPPort()), nil)
		require.NoError(t, err)

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h2cClient.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusNoContent, res.StatusCode)

		assert.Equal(t, 2, res.ProtoMajor)
	})

	t.Run("server allows upgrading from HTTP1 to HTTP2 Cleartext", func(t *testing.T) {
		// The Go HTTP client doesn't handle upgrades from HTTP/1 to HTTP/2
		// The best we can do for now is to verify that the server responds with a 101 response when we signal that we are able to upgrade
		// See: https://github.com/golang/go/issues/46249

		reqCtx, reqCancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer reqCancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/healthz", h.proc.HTTPPort()), nil)
		require.NoError(t, err)

		req.Header.Set("Connection", "Upgrade, HTTP2-Settings")
		req.Header.Set("Upgrade", "h2c")
		req.Header.Set("HTTP2-Settings", "AAMAAABkAARAAAAAAAIAAAAA")

		// Body is closed below but the linter isn't seeing that
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, res.Body.Close())
		})

		// This response should have arrived over HTTP/1
		assert.Equal(t, 1, res.ProtoMajor)
		assert.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
		assert.NotEmpty(t, res.Header)
		assert.Equal(t, "Upgrade", res.Header.Get("Connection"))
		assert.Equal(t, "h2c", res.Header.Get("Upgrade"))
	})
}

// Drain body before closing
func closeBody(body io.ReadCloser) error {
	_, err := io.Copy(io.Discard, body)
	if err != nil {
		return err
	}
	return body.Close()
}
