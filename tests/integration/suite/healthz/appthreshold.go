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

package healthz

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appThreshold))
}

// appThreshold tests that the app health threshold counts only consecutive
// probe failures: a successful probe resets the failure count.
type appThreshold struct {
	daprd  *procdaprd.Daprd
	probes atomic.Int64
}

func (a *appThreshold) Setup(t *testing.T) []framework.Option {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Fail every second probe, so that no two consecutive probes fail.
		if a.probes.Add(1)%2 == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/myfunc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
	})

	srv := prochttp.New(t, prochttp.WithHandler(mux))
	a.daprd = procdaprd.New(t,
		procdaprd.WithAppHealthCheck(true),
		procdaprd.WithAppHealthCheckPath("/healthz"),
		procdaprd.WithAppPort(srv.Port()),
		procdaprd.WithAppHealthProbeInterval(1),
		procdaprd.WithAppHealthProbeThreshold(3),
	)

	return []framework.Option{
		framework.WithProcesses(a.daprd, srv),
	}
}

func (a *appThreshold) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", a.daprd.HTTPPort(), a.daprd.AppID())
	httpClient := client.HTTP(t)

	invoke := func() (int, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(body)
	}

	// The first probe succeeds and marks the app healthy.
	require.Eventually(t, func() bool {
		status, body := invoke()
		return status == http.StatusOK && body == "GET /myfunc"
	}, time.Second*10, 10*time.Millisecond, "expected dapr to report app healthy after the first probe")

	// With a threshold of 3, the third failed probe would take the app out of
	// service if failures were counted across successful probes. Keep invoking
	// the app until five probes have failed: it must stay healthy throughout.
	const wantProbes = 11
	deadline := time.Now().Add(time.Second * 30)
	for a.probes.Load() < wantProbes {
		require.True(t, time.Now().Before(deadline), "expected %d app health probes before the deadline", wantProbes)

		status, body := invoke()
		require.Equal(t, http.StatusOK, status, "app reported unhealthy after %d probes: %s", a.probes.Load(), body)
		require.Equal(t, "GET /myfunc", body)

		time.Sleep(10 * time.Millisecond)
	}
}
