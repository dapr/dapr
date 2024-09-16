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
	// "io"
	"net/http"
	// "strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(defaultcircuitbreaker))
}

type defaultcircuitbreaker struct {
	daprdClient    *daprd.Daprd
	daprdServer    *daprd.Daprd
	callCount *atomic.Int32
}

func (d *defaultcircuitbreaker) Setup(t *testing.T) []framework.Option {
	d.callCount = &atomic.Int32{}

	handler := http.NewServeMux()
	handler.HandleFunc("/circuitbreaker_ok", func(w http.ResponseWriter, r *http.Request) {
		d.callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler.HandleFunc("/circuitbreaker_fail", func(w http.ResponseWriter, r *http.Request) {
		d.callCount.Add(1)
		if d.callCount.Load() <= 4 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))

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
        trip: requests > 3
  targets:
    apps:
      server:
        circuitBreaker: DefaultCircuitBreaker
`

	d.daprdClient = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithResourceFiles(resiliency),
		daprd.WithAppID("client"),
	)
	d.daprdServer = daprd.New(t,
		daprd.WithAppPort(srv.Port()),
		daprd.WithAppID("server"),
		
	)
	// log.Print(d.daprdClient.Metrics(t, context.Background()))

	return []framework.Option{
		framework.WithProcesses(srv, d.daprdClient, d.daprdServer),
	}
}

func (d *defaultcircuitbreaker) Run(t *testing.T, ctx context.Context) {
	d.daprdClient.WaitUntilRunning(t, ctx)
	d.daprdServer.WaitUntilRunning(t, ctx)

	t.Run("circuit breaker opens after consecutive failures", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/circuitbreaker_ok", d.daprdClient.HTTPPort(), d.daprdServer.AppID())

		// First 3 calls should fail
		for i := 0; i < 4; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := util.HTTPClient(t).Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// 4th call should not circuit breaker
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		
		// assert cb execution,activation,and current state counts
		mtc := d.daprdClient.Metrics(t, context.Background())
		assert.Equal(t, float64(5), mtc["dapr_resiliency_count|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"])
		assert.Equal(t, float64(0), mtc["dapr_resiliency_activations_total|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"])
		//TODO: the key has been stored, meaning the metric has recorded before, but even forcing a value of 1 shows only values of 0
		// assert.Equal(t, mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], float64(1))

		// Verify the total number of calls made to the server
		assert.Equal(t, int32(5), d.callCount.Load())
	})

	t.Run("circuit breaker opens after consecutive failures", func(t *testing.T) {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/circuitbreaker_fail", d.daprdClient.HTTPPort(), d.daprdServer.AppID())

		// First 3 calls should fail
		for i := 0; i < 4; i++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := util.HTTPClient(t).Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		// 4th call should trigger circuit breaker, even though the server would have returned a 200
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		
		// assert cb execution,activation,and current state counts
		mtc := d.daprdClient.Metrics(t, context.Background())
		assert.Equal(t, float64(4), mtc["dapr_resiliency_count|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:closed|target:app_server"])
		assert.Equal(t, float64(1), mtc["dapr_resiliency_activations_total|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"])
		//TODO: the key has been stored, meaning the metric has recorded before, but even forcing a value of 1 shows only values of 0
		// assert.Equal(t, mtc["dapr_resiliency_cb_state|app_id:client|flow_direction:outbound|name:myresiliency|namespace:|policy:circuitbreaker|status:open|target:app_server"], float64(1))

		// Wait for the circuit breaker to transition to half-open state
		time.Sleep(6 * time.Second)

		// This call should succeed and close the circuit
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err = util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		// Subsequent calls should succeed
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err = util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		// Verify the total number of calls made to the server
		assert.Equal(t, int32(6), d.callCount.Load())
	})
}
