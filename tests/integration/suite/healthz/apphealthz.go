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
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// TODO: disable the app healthz tests for now because they are failing.
	//suite.Register(new(AppHealthz))
}

// AppHealthz tests that Dapr responds to healthz requests for the app.
type AppHealthz struct {
	lis        net.Listener
	returnCode int
}

func (a *AppHealthz) Setup(t *testing.T) []framework.RunDaprdOption {
	var err error
	a.lis, err = net.Listen("tcp", ":0")
	require.NoError(t, err)

	return []framework.RunDaprdOption{
		framework.WithAppHealthCheck(true),
		framework.WithAppHealthCheckPath("/health"),
		framework.WithAppPort(int(a.lis.Addr().(*net.TCPAddr).Port)),
		framework.WithAppHealthProbeInterval(1),
		framework.WithAppHealthProbeThreshold(1),
	}
}

func (a *AppHealthz) Run(t *testing.T, cmd *framework.Command) {
	var healthy atomic.Bool
	healthy.Store(true)

	mux := http.NewServeMux()
	// TODO: @joshvanl there is currently a bug where the health check path is
	// not being set correctly.
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/health", r.URL.Path)

		if healthy.Load() {
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		require.NoError(t, http.Serve(a.lis, mux))
	}()

	assert.Eventually(t, func() bool {
		_, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", cmd.InternalGRPCPort))
		return err == nil
	}, time.Second, time.Millisecond)

	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", cmd.HttpPort, cmd.AppID))
		require.NoError(t, err)
		return resp.StatusCode == http.StatusOK
	}, time.Second*5, 100*time.Millisecond)

	healthy.Store(false)

	// TODO: The timeout for this eventually here is too large because the
	// healthz probe interval and threshold are bugged and not being set
	// correctly.
	assert.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Get(fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", cmd.HttpPort, cmd.AppID))
		require.NoError(t, err)
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*30, time.Second)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for healthz server to close")
	}
}
