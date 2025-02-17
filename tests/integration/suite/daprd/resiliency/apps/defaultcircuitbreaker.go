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

package apps

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(defaultcircuitbreaker))
}

type defaultcircuitbreaker struct {
	daprdClient *daprd.Daprd
	daprdServer *daprd.Daprd
	callCount1  *atomic.Int32
	callCount2  *atomic.Int32
}

func (d *defaultcircuitbreaker) Setup(t *testing.T) []framework.Option {
	d.callCount1 = &atomic.Int32{}
	d.callCount2 = &atomic.Int32{}

	app := app.New(t,
		app.WithHandlerFunc("/circuitbreaker_ok", func(w http.ResponseWriter, r *http.Request) {
			d.callCount1.Add(1)
			w.WriteHeader(http.StatusOK)
		}),
		app.WithHandlerFunc("/circuitbreaker_fail", func(w http.ResponseWriter, r *http.Request) {
			d.callCount2.Add(1)
			if d.callCount2.Load() <= 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
	)

	resiliency := `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    circuitBreakers:
      DefaultCircuitBreaker:
        maxRequests: 1
        interval: 0
        timeout: 5s
        trip: consecutiveFailures >= 3
  targets:
    apps:
      server:
        circuitBreaker: DefaultCircuitBreaker
`

	d.daprdClient = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithResourceFiles(resiliency),
		daprd.WithAppID("client"),
		daprd.WithLogLevel("debug"),
	)
	d.daprdServer = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppID("server"),
	)

	return []framework.Option{
		framework.WithProcesses(app, d.daprdClient, d.daprdServer),
	}
}

func (d *defaultcircuitbreaker) Run(t *testing.T, ctx context.Context) {
	d.daprdClient.WaitUntilRunning(t, ctx)
	d.daprdServer.WaitUntilAppHealth(t, ctx)

	t.Run("circuit breaker does not open after consecutive successes", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/circuitbreaker_ok", d.daprdClient.HTTPPort(), d.daprdServer.AppID())

		// First 3 calls should pass
		for i := 1; i <= 3; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := client.HTTP(t).Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// 4th call should not trigger circuit breaker
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// assert cb execution,activation,and current state counts
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := d.daprdClient.Metrics(c, context.Background()).All()
			assert.InDelta(c, float64(4), mtc["dapr_resiliency_count|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"], 0)
			assert.InDelta(c, float64(0), mtc["dapr_resiliency_activations_total|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], 0)
			assert.InDelta(c, float64(1), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"], 0)
		}, time.Second*4, 10*time.Millisecond)

		// Verify the total number of calls made to the server
		assert.Equal(t, int32(4), d.callCount1.Load())
	})

	t.Run("circuit breaker opens after consecutive failures", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/circuitbreaker_fail", d.daprdClient.HTTPPort(), d.daprdServer.AppID())

		// First 3 calls should fail
		for i := 1; i <= 3; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := client.HTTP(t).Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// 4th call should trigger circuit breaker, even though the server would have returned a 200
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		// assert cb execution,activation,and current state counts
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := d.daprdClient.Metrics(c, context.Background()).All()
			assert.InDelta(c, float64(7), mtc["dapr_resiliency_count|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"], 0)
			assert.InDelta(c, float64(1), mtc["dapr_resiliency_activations_total|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], 0)
			assert.InDelta(c, float64(1), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], 0)
		}, time.Second*4, 10*time.Millisecond)

		// Wait for the circuit breaker to be able to transition to half-open state
		time.Sleep(6 * time.Second)

		// This call should succeed and close the circuit
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err = client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		// make sure gauge is half open
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := d.daprdClient.Metrics(c, context.Background()).All()
			assert.InDelta(c, float64(0), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], 0)
			assert.InDelta(c, float64(1), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:half-open|target:app_server"], 0)
		}, time.Second*4, 10*time.Millisecond)

		// Subsequent calls should succeed
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err = client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		// make sure gauge is closed
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := d.daprdClient.Metrics(c, context.Background()).All()
			assert.InDelta(c, float64(0), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:half-open|target:app_server"], 0)
			assert.InDelta(c, float64(1), mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"], 0)
		}, time.Second*4, 10*time.Millisecond)

		// Verify the total number of calls made to the server
		assert.Equal(t, int32(5), d.callCount2.Load())
	})
}
