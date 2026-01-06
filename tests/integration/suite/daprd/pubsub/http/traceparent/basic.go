/*
Copyright 2025 The Dapr Authors
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

package traceparent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
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
	suite.Register(new(basic))
}

type basic struct {
	daprd    *daprd.Daprd
	headerCh chan nethttp.Header
}

func (a *basic) Setup(t *testing.T) []framework.Option {
	a.headerCh = make(chan nethttp.Header, 1)

	app := app.New(t,
		app.WithHandlerFunc("/test-topic", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			a.headerCh <- r.Header.Clone()
			var cloudEvent map[string]interface{}
			err := json.NewDecoder(r.Body).Decode(&cloudEvent)
			assert.NoError(t, err)

			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			subs := []map[string]interface{}{
				{
					"pubsubname": "mypub",
					"topic":      "test-topic",
					"route":      "/test-topic",
				},
			}
			json.NewEncoder(w).Encode(subs)
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, a.daprd),
	}
}

func (a *basic) Run(t *testing.T, ctx context.Context) {
	t.Run("With traceparent header", func(t *testing.T) {
		a.daprd.WaitUntilRunning(t, ctx)

		httpClient := client.HTTP(t)
		pubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/test-topic", a.daprd.HTTPPort())
		payload := []byte(`{"message": "hello from http"}`)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, pubURL, bytes.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02"
		req.Header.Set("traceparent", tp)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		resp.Body.Close()

		select {
		case headers := <-a.headerCh:
			traceparent := headers.Get("traceparent")
			assert.Equal(t, tp, traceparent)
		case <-time.After(time.Second * 10):
			assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
		}
	})

	t.Run("Without traceparent header", func(t *testing.T) {
		a.daprd.WaitUntilRunning(t, ctx)

		httpClient := client.HTTP(t)
		pubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/test-topic", a.daprd.HTTPPort())
		payload := []byte(`{"message": "hello from http"}`)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, pubURL, bytes.NewReader(payload))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
		resp.Body.Close()

		select {
		case headers := <-a.headerCh:
			traceparent := headers.Get("traceparent")

			assert.Equal(t, "00-00000000000000000000000000000000-0000000000000000-00", traceparent)
		case <-time.After(time.Second * 10):
			assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
		}
	})
}
