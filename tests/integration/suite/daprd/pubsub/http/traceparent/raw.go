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
	suite.Register(new(raw))
}

type raw struct {
	daprd    *daprd.Daprd
	headerCh chan nethttp.Header
}

func (r *raw) Setup(t *testing.T) []framework.Option {
	r.headerCh = make(chan nethttp.Header, 2)

	app := app.New(t,
		app.WithHandlerFunc("/test-topic", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			r.headerCh <- req.Header.Clone()
			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
		app.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, req *nethttp.Request) {
			json.NewEncoder(w).Encode([]map[string]any{{
				"pubsubname": "mypub",
				"topic":      "test-topic",
				"route":      "/test-topic",
				"metadata":   map[string]string{"rawPayload": "true"},
			}})
		}),
	)

	r.daprd = daprd.New(t,
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
		framework.WithProcesses(app, r.daprd),
	}
}

func (r *raw) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	publish := func(t *testing.T, path string, body []byte) {
		t.Helper()
		url := fmt.Sprintf("http://localhost:%d/%s?metadata.rawPayload=true", r.daprd.HTTPPort(), path)
		req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("traceparent", tp)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		require.Less(t, resp.StatusCode, 300)
		resp.Body.Close()
	}

	assertTrace := func(t *testing.T, n int) {
		t.Helper()
		for range n {
			select {
			case headers := <-r.headerCh:
				traceparent := headers.Get("traceparent")
				require.Len(t, traceparent, len(tp))
				assert.Equal(t, tp[3:35], traceparent[3:35])
			case <-time.After(time.Second * 10):
				assert.Fail(t, "Timed out waiting for pubsub event to be delivered to app")
			}
		}
	}

	t.Run("publish", func(t *testing.T) {
		publish(t, "v1.0/publish/mypub/test-topic", []byte(`{"message": "hello"}`))
		assertTrace(t, 1)
	})

	t.Run("bulk publish", func(t *testing.T) {
		publish(t, "v1.0-alpha1/publish/bulk/mypub/test-topic", []byte(`[
			{"entryId": "1", "event": {"message": "one"}, "contentType": "application/json"},
			{"entryId": "2", "event": {"message": "two"}, "contentType": "application/json"}
		]`))
		assertTrace(t, 2)
	})
}
