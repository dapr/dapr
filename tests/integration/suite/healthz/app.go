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
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procapp "github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(app))
}

// app tests that Dapr responds to healthz requests for the app.
type app struct {
	daprd   *procdaprd.Daprd
	healthy atomic.Bool
	app     *procapp.App
}

func (a *app) Setup(t *testing.T) []framework.Option {
	a.healthy.Store(true)

	a.app = procapp.New(t,
		procapp.WithHandlerFunc("/foo", func(w http.ResponseWriter, r *http.Request) {
			if a.healthy.Load() {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
		}),
		procapp.WithHandlerFunc("/myfunc", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
		}),
	)

	a.daprd = procdaprd.New(t,
		procdaprd.WithAppHealthCheck(true),
		procdaprd.WithAppHealthCheckPath("/foo"),
		procdaprd.WithAppPort(a.app.Port()),
		procdaprd.WithAppHealthProbeInterval(1),
		procdaprd.WithAppHealthProbeThreshold(1),
	)

	return []framework.Option{
		framework.WithProcesses(a.app, a.daprd),
	}
}

func (a *app) Run(t *testing.T, ctx context.Context) {
	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/myfunc", a.daprd.HTTPPort(), a.daprd.AppID())

	a.healthy.Store(true)

	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusOK && string(body) == "GET /myfunc"
	}, time.Second*5, 10*time.Millisecond, "expected dapr to report app healthy when /foo returns 200")

	a.healthy.Store(false)

	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode == http.StatusInternalServerError &&
			strings.Contains(string(body), "app is not in a healthy state")
	}, time.Second*20, 10*time.Millisecond, "expected dapr to report app unhealthy now /foo returns 503")
}
