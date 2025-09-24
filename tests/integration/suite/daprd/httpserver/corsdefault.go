/*
Copyright 2025 The Dapr Authors
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
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(corsdefault))
}

type corsdefault struct {
	proc *procdaprd.Daprd
}

func (h *corsdefault) Setup(t *testing.T) []framework.Option {
	h.proc = procdaprd.New(t)
	return []framework.Option{
		framework.WithProcesses(h.proc),
	}
}

func (h *corsdefault) Run(t *testing.T, ctx context.Context) {
	h.proc.WaitUntilRunning(t, ctx)

	h1Client := client.HTTP(t)
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

	t.Run("OPTIONS", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodOptions, fmt.Sprintf("http://localhost:%d/v1.0/metadata", h.proc.HTTPPort()), nil)
		require.NoError(t, err)
		req.Header.Set("Origin", "https://test.com")
		req.Header.Set("Access-Control-Request-Method", "GET")

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
		require.NotEmpty(t, res.Header.Get("Vary"))
	})

	t.Run("OPTIONS, unnecessary token", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodOptions, fmt.Sprintf("http://localhost:%d/v1.0/metadata", h.proc.HTTPPort()), nil)
		require.NoError(t, err)
		req.Header.Set("Origin", "*")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("dapr-api-token", "foo")

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
		require.NotEmpty(t, res.Header.Get("Vary"))
	})

	t.Run("GET", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", h.proc.HTTPPort()), nil)
		require.NoError(t, err)

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.Empty(t, res.Header.Get("Access-Control-Allow-Origin"))
	})

	t.Run("GET, unnecessary token", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/v1.0/metadata", h.proc.HTTPPort()), nil)
		require.NoError(t, err)
		req.Header.Set("dapr-api-token", "test")

		// Body is closed below but the linter isn't seeing that
		//nolint:bodyclose
		res, err := h1Client.Do(req)
		require.NoError(t, err)
		defer closeBody(res.Body)
		require.Equal(t, http.StatusOK, res.StatusCode)

		require.Empty(t, res.Header.Get("Access-Control-Allow-Origin"))
	})
}
