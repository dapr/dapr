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
	"strings"
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
	suite.Register(new(resiliencynoretry))
}

// resiliencynoretry tests that streaming requests (chunked-transfer) are NOT
// retried even when a resiliency policy with retries is configured, because
// the streaming body cannot be buffered for replay.
type resiliencynoretry struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	endpointCalls atomic.Int32
}

func (c *resiliencynoretry) Setup(t *testing.T) []framework.Option {
	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/always-fail", func(w http.ResponseWriter, r *http.Request) {
			c.endpointCalls.Add(1)
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":"always fails"}`))
		}),
	)

	c.daprdReceiver = daprd.New(t,
		daprd.WithAppPort(receiverApp.Port()),
	)

	resiliency := fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: myresiliency
spec:
  policies:
    retries:
      retrypolicy:
        policy: constant
        duration: 1ms
        maxRetries: 3
  targets:
    apps:
      %s:
        retry: retrypolicy
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

func (c *resiliencynoretry) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("streaming request is not retried despite resiliency policy", func(t *testing.T) {
		// Streaming requests (unknown Content-Length) must not be retried
		// because the body cannot be buffered for replay. The request
		// should be attempted exactly once.
		pr, pw := io.Pipe()
		go func() {
			pw.Write([]byte("streaming data"))
			pw.Close()
		}()

		c.endpointCalls.Store(0)
		url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/always-fail",
			c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, pr)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		// Should be called exactly once — no retries for streaming requests
		assert.Equal(t, int32(1), c.endpointCalls.Load())
	})

	t.Run("fixed-length request is retried per resiliency policy", func(t *testing.T) {
		// Contrast: fixed-length (known Content-Length) requests ARE retried
		// per the resiliency policy (1 initial + 3 retries = 4 total calls).
		c.endpointCalls.Store(0)
		url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/always-fail",
			c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("fixed body"))
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		// Should be called 4 times: 1 initial + 3 retries
		assert.Equal(t, int32(4), c.endpointCalls.Load())
	})
}
