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
	suite.Register(new(resiliencyrecovery))
}

type resiliencyrecovery struct {
	daprdSender   *daprd.Daprd
	daprdReceiver *daprd.Daprd

	endpointCalls atomic.Int32
}

func (c *resiliencyrecovery) Setup(t *testing.T) []framework.Option {
	receiverApp := app.New(t,
		app.WithHandlerFunc("/healthz", func(http.ResponseWriter, *http.Request) {}),
		app.WithHandlerFunc("/flaky", func(w http.ResponseWriter, r *http.Request) {
			n := c.endpointCalls.Add(1)
			io.Copy(io.Discard, r.Body)
			// Fail the first 3 calls, succeed on the 4th
			if n <= 3 {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"transient failure"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"recovered"}`))
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
        maxRetries: 5
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

func (c *resiliencyrecovery) Run(t *testing.T, ctx context.Context) {
	c.daprdSender.WaitUntilRunning(t, ctx)
	c.daprdReceiver.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	t.Run("fixed-length request recovers from transient failure", func(t *testing.T) {
		c.endpointCalls.Store(0)

		url := fmt.Sprintf("http://%s/v1.0/invoke/%s/method/flaky",
			c.daprdSender.HTTPAddress(), c.daprdReceiver.AppID())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"message":"hello"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should recover: 3 failures + 1 success = 200
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"status":"recovered"}`, string(body))
		// 3 failed + 1 successful = 4 total calls
		assert.Equal(t, int32(4), c.endpointCalls.Load())
	})
}
