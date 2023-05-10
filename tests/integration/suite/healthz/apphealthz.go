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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	// TODO: disable the app healthz tests for now because they are failing.
	suite.Register(new(AppHealthz))
}

// AppHealthz tests that Dapr responds to healthz requests for the app.
type AppHealthz struct {
	healthy atomic.Bool
	server  http.Server
	done    chan struct{}
}

func (a *AppHealthz) Setup(t *testing.T, _ context.Context) []framework.RunDaprdOption {
	a.healthy.Store(true)
	a.done = make(chan struct{})

	mux := http.NewServeMux()
	// TODO: @joshvanl there is currently a bug where the health check path is
	// not being set correctly.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/healthz", r.URL.Path)

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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	a.server = http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		defer close(a.done)
		require.ErrorIs(t, a.server.Serve(listener), http.ErrServerClosed)
	}()

	return []framework.RunDaprdOption{
		framework.WithAppHealthCheck(true),
		framework.WithAppHealthCheckPath("/healthz"),
		framework.WithAppPort(listener.Addr().(*net.TCPAddr).Port),
		framework.WithAppHealthProbeInterval(1),
		framework.WithAppHealthProbeThreshold(1),
	}
}

func (a *AppHealthz) Run(t *testing.T, ctx context.Context, cmd *framework.Command) {
	assert.Eventually(t, func() bool {
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", cmd.InternalGRPCPort))
		return err == nil
	}, time.Second, time.Millisecond)

	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", cmd.HTTPPort, cmd.AppID))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK
	}, time.Second*5, 100*time.Millisecond)

	a.healthy.Store(false)

	// TODO: The timeout for this eventually here is too large because the
	// healthz probe interval and threshold are bugged and not being set
	// correctly.
	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", cmd.HTTPPort, cmd.AppID))
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*30, time.Second)

	require.NoError(t, a.server.Shutdown(ctx))

	select {
	case <-a.done:
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for healthz server to close")
	}
}
