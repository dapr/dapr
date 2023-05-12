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

package healthz

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(app))
}

// app tests that Dapr responds to healthz requests for the app.
type app struct {
	daprd    *procdaprd.Daprd
	healthy  atomic.Bool
	server   http.Server
	listener net.Listener
}

func (a *app) Setup(t *testing.T) []framework.Option {
	a.healthy.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/foo", r.URL.Path)

		if a.healthy.Load() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})

	mux.HandleFunc("/myfunc", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/myfunc", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})

	var err error
	a.listener, err = net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	a.server = http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	a.daprd = procdaprd.New(t,
		procdaprd.WithAppHealthCheck(true),
		procdaprd.WithAppHealthCheckPath("/foo"),
		procdaprd.WithAppPort(a.listener.Addr().(*net.TCPAddr).Port),
		procdaprd.WithAppHealthProbeInterval(1),
		procdaprd.WithAppHealthProbeThreshold(1),
	)

	return []framework.Option{
		framework.WithProcesses(a.daprd),
	}
}

func (a *app) Run(t *testing.T, ctx context.Context) {
	done := make(chan struct{})

	go func() {
		defer close(done)
		require.ErrorIs(t, a.server.Serve(a.listener), http.ErrServerClosed)
	}()

	assert.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", a.daprd.InternalGRPCPort))
		if err != nil {
			return false
		}
		require.NoError(t, conn.Close())
		return true
	}, time.Second*5, time.Millisecond)

	a.healthy.Store(true)

	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", a.daprd.HTTPPort, a.daprd.AppID))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, time.Second*20, 50*time.Millisecond, "expected dapr to report app healthy when /foo returns 200")

	a.healthy.Store(false)

	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", a.daprd.HTTPPort, a.daprd.AppID))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*20, 50*time.Millisecond, "expected dapr to report app unhealthy now /foo returns 503")

	require.NoError(t, a.server.Shutdown(ctx))

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for healthz server to close")
	}
}
