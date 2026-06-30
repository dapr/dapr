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

package metrics

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
	suite.Register(new(resiliencycbstate))
}

// resiliencycbstate tests that dapr_resiliency_cb_state gauge updates immediately
// on every circuit breaker state transition (fix for github.com/dapr/dapr/issues/10113).
// Before the fix the gauge was only updated when the resiliency policy was next
// instantiated, so it could remain stuck at "half-open" even after the probe
// succeeded and the breaker had already closed.
type resiliencycbstate struct {
	daprdClient *daprd.Daprd
	daprdServer *daprd.Daprd
	callCount   atomic.Int32
}

func (r *resiliencycbstate) Setup(t *testing.T) []framework.Option {
	testApp := app.New(t,
		app.WithHandlerFunc("/cbtest", func(w http.ResponseWriter, _ *http.Request) {
			if r.callCount.Add(1) <= 3 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}),
	)

	r.daprdServer = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppID("cbstate-server"),
	)

	// timeout:1s keeps the test fast: the CB moves to half-open 1 second after
	// opening, allowing us to send a probe without a long sleep.
	resiliency := fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: cbstatepolicy
spec:
  policies:
    circuitBreakers:
      cbpolicy:
        maxRequests: 1
        interval: 0
        timeout: 1s
        trip: consecutiveFailures >= 3
  targets:
    apps:
      %s:
        circuitBreaker: cbpolicy
`, r.daprdServer.AppID())

	r.daprdClient = daprd.New(t,
		daprd.WithAppPort(testApp.Port()),
		daprd.WithAppID("cbstate-client"),
		daprd.WithResourceFiles(resiliency),
	)

	return []framework.Option{
		framework.WithProcesses(testApp, r.daprdServer, r.daprdClient),
	}
}

func (r *resiliencycbstate) Run(t *testing.T, ctx context.Context) {
	r.daprdClient.WaitUntilRunning(t, ctx)
	r.daprdServer.WaitUntilAppHealth(t, ctx)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/cbtest",
		r.daprdClient.HTTPPort(), r.daprdServer.AppID())

	cbStateKey := func(status string) string {
		return fmt.Sprintf(
			"dapr_resiliency_cb_state|app_id:cbstate-client|flow_direction:outbound|name:cbstatepolicy|namespace:|policy:circuitbreaker|status:%s|target:app_cbstate-server",
			status,
		)
	}

	t.Run("gauge shows open after consecutive failures", func(t *testing.T) {
		for range 3 {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
			require.NoError(t, err)
			resp, err := client.HTTP(t).Do(req)
			require.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
		}

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := r.daprdClient.Metrics(c, ctx).All()
			assert.InDelta(c, float64(0), mtc[cbStateKey("closed")], 0)
			assert.InDelta(c, float64(1), mtc[cbStateKey("open")], 0)
		}, time.Second*4, 10*time.Millisecond)
	})

	t.Run("gauge immediately reflects closed after probe succeeds", func(t *testing.T) {
		// Wait for the breaker's timeout so it enters half-open and allows one probe.
		time.Sleep(2 * time.Second)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		require.NoError(t, err)
		resp, err := client.HTTP(t).Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		require.NoError(t, resp.Body.Close())

		// The gauge must reflect "closed" without waiting for the next policy
		// instantiation. Before fix #10113, it would stay stuck at "half-open"
		// here even though the breaker had already transitioned to closed.
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			mtc := r.daprdClient.Metrics(c, ctx).All()
			assert.InDelta(c, float64(0), mtc[cbStateKey("open")], 0)
			assert.InDelta(c, float64(0), mtc[cbStateKey("half-open")], 0)
			assert.InDelta(c, float64(1), mtc[cbStateKey("closed")], 0)
		}, time.Second*4, 10*time.Millisecond)
	})
}
