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

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(errors))
}

type errors struct {
	daprd     *daprd.Daprd
	appHealth atomic.Bool
}

func (e *errors) Setup(t *testing.T) []framework.Option {
	e.appHealth.Store(true)

	app := prochttp.New(t, prochttp.WithHandlerFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if e.appHealth.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	e.daprd = daprd.New(t,
		daprd.WithAppID("health-errors"),
		daprd.WithAppPort(app.Port()),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthCheckPath("/healthz"),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithErrorCodeMetrics(t),
	)

	return []framework.Option{
		framework.WithProcesses(app, e.daprd),
	}
}

func (e *errors) Run(t *testing.T, ctx context.Context) {
	httpClient := client.HTTP(t)

	t.Run("ERR_HEALTH_NOT_READY", func(t *testing.T) {
		e.daprd.WaitUntilTCPReady(t, ctx)

		seenErr := assert.Eventually(t, func() bool {
			statusCode, body := requestHealth(t, ctx, httpClient, e.daprd.HTTPPort(), "v1.0/healthz")
			if statusCode == http.StatusInternalServerError {
				return body["errorCode"] == "ERR_HEALTH_NOT_READY"
			}
			return false
		}, 10*time.Second, 20*time.Millisecond)
		assert.True(t, seenErr, "expected to observe ERR_HEALTH_NOT_READY during startup")

		e.daprd.WaitUntilRunning(t, ctx)
	})

	t.Run("ERR_HEALTH_APPID_NOT_MATCH", func(t *testing.T) {
		statusCode, body := requestHealth(t, ctx, httpClient, e.daprd.HTTPPort(), "v1.0/healthz?appid=other-app")
		require.Equal(t, http.StatusInternalServerError, statusCode)
		require.Equal(t, "ERR_HEALTH_APPID_NOT_MATCH", body["errorCode"])
		require.Equal(t, "dapr app-id does not match", body["message"])

		details, ok := body["details"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, details)
		assert.Contains(t, fmt.Sprintf("%v", details), "field_violations")
	})

	t.Run("ERR_OUTBOUND_HEALTH_NOT_READY", func(t *testing.T) {
		e.appHealth.Store(false)
		t.Cleanup(func() { e.appHealth.Store(true) })

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			statusCode, body := requestHealth(t, ctx, httpClient, e.daprd.HTTPPort(), "v1.0/healthz/outbound")
			if statusCode != http.StatusInternalServerError {
				assert.Fail(c, "outbound endpoint not returning 500 yet")
				return
			}
			assert.Equal(c, "ERR_OUTBOUND_HEALTH_NOT_READY", body["errorCode"])
			assert.Equal(c, "dapr outbound is not ready", body["message"])
			assert.Contains(c, fmt.Sprintf("%v", body["details"]), "ERR_OUTBOUND_HEALTH_NOT_READY")
		}, 15*time.Second, 50*time.Millisecond)
	})
}

func requestHealth(t *testing.T, ctx context.Context, c *http.Client, port int, path string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/%s", port, path), nil)
	require.NoError(t, err)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	out := map[string]any{}
	if resp.StatusCode >= http.StatusBadRequest {
		b, readErr := io.ReadAll(resp.Body)
		require.NoError(t, readErr)
		require.NoError(t, json.Unmarshal(b, &out))
	}

	return resp.StatusCode, out
}
