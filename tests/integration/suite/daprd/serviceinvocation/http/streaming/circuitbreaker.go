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

package streaming

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(circuitbreaker))
}

// circuitbreaker tests that circuit breakers correctly trip for streaming
// requests. Even though streaming requests cannot be retried, the circuit
// breaker should still count consecutive failures and open the circuit.
type circuitbreaker struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	endpointCalls atomic.Int32
}

func (c *circuitbreaker) Setup(t *testing.T) []framework.Option {
	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/always-fail", func(w http.ResponseWriter, r *http.Request) {
			c.endpointCalls.Add(1)
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)

	c.daprdReceiver = daprd.New(t,
		daprd.WithAppPort(receiverApp.Port()),
	)

	resiliency := fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: streamingcb
spec:
  policies:
    circuitBreakers:
      cbpolicy:
        maxRequests: 1
        interval: 0
        timeout: 5s
        trip: consecutiveFailures >= 3
  targets:
    apps:
      %s:
        circuitBreaker: cbpolicy
`, c.daprdReceiver.AppID())

	senderApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
	)
	c.daprdSender = daprd.New(t,
		daprd.WithAppPort(senderApp.Port()),
		daprd.WithResourceFiles(resiliency),
	)

	return []framework.Option{
		framework.WithProcesses(receiverApp, senderApp, c.daprdReceiver, c.daprdSender),
	}
}

func (c *circuitbreaker) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	// Send 3 streaming requests that all fail with 500.
	// The circuit breaker should count these failures.
	for i := range 3 {
		pr, pw := io.Pipe()
		go func() {
			fmt.Fprintf(pw, "streaming data %d", i)
			pw.Close()
		}()

		url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/always-fail",
			c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		resp.Body.Close()
	}

	// All 3 requests should have reached the endpoint (no retries for
	// streaming, but each request is a separate call).
	assert.Equal(t, int32(3), c.endpointCalls.Load())

	// 4th streaming request: the circuit breaker should be open, so the
	// request should fail without reaching the endpoint.
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("this should be blocked"))
		pw.Close()
	}()

	url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/always-fail",
		c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
	require.NoError(t, err)

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Circuit is open — request should fail without reaching the endpoint
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	// Endpoint call count should still be 3 (4th request was blocked)
	assert.Equal(t, int32(3), c.endpointCalls.Load())
}
